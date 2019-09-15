package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	mutatorstime "github.com/SuddenSelect/k8s-ac/mutators/time"
	validatorsimage "github.com/SuddenSelect/k8s-ac/validators/image"
	"io/ioutil"
	"k8s.io/api/admission/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	authv1 "k8s.io/api/authentication/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/klog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Logging Conventions:
// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md

func main() {
	klog.InitFlags(nil)
	pemKey := *flag.String("key", "server.key", "TLS key file in PEM format")
	pemCert := *flag.String("cert", "server.pem", "TLS cert file in PEM format")
	flag.Parse()
	if _, err := os.Stat(pemKey); os.IsNotExist(err) {
		klog.Fatal(err)
	}
	if _, err := os.Stat(pemCert); os.IsNotExist(err) {
		klog.Fatal(err)
	}

	serverHandler := http.NewServeMux()
	serverHandler.Handle(newAdmissionRequestHandler("/inject-localtime", mutatorstime.InjectNodeLocaltime))
	serverHandler.Handle(newAdmissionRequestHandler("/image-not-latest", validatorsimage.ImageVersionIsNotLatest))
	server := newInClusterServer(serverHandler)
	go func() {
		if err := server.ListenAndServeTLS(pemCert, pemKey); err != nil {
			klog.Fatal(err)
		}
	}()

	klog.Info("k8s-ac started")

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	ctx, cancelFunc := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelFunc()
	if err := server.Shutdown(ctx); err != nil {
		klog.Fatal(err)
	}
	klog.Flush()
}

func newInClusterServer(serverHandler http.Handler) *http.Server {
	kubernetesCA, err := os.Open("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	if err != nil {
		klog.Fatal(err)
	}
	kubernetesCAbytes, err := ioutil.ReadAll(kubernetesCA)
	if err != nil {
		klog.Fatal(err)
	}
	trustedCertPool := x509.NewCertPool()
	trustedCertPool.AppendCertsFromPEM(kubernetesCAbytes)

	return &http.Server{
		Addr:    ":8443",
		Handler: serverHandler,
		TLSConfig: &tls.Config{
			RootCAs: trustedCertPool,
		},
	}
}

type admissionFunc = func(request *v1beta1.AdmissionRequest) (*v1beta1.AdmissionResponse, error)

type admissionRequestHandler struct {
	prefix            string
	admissionFunction admissionFunc
}

func newAdmissionRequestHandler(prefix string, admissionFunction admissionFunc) (string, *admissionRequestHandler) {
	return prefix, &admissionRequestHandler{
		prefix:            fmt.Sprintf("[%v] ", prefix),
		admissionFunction: admissionFunction,
	}
}

func (arh *admissionRequestHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	httpBody, err := ioutil.ReadAll(request.Body)
	if err != nil {
		klog.Error(arh.prefix, err)
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	klog.V(5).Info(arh.prefix, string(httpBody))

	universalDeserializer := serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()
	var admissionReview v1beta1.AdmissionReview
	if _, _, err := universalDeserializer.Decode(httpBody, nil, &admissionReview); err != nil {
		klog.V(2).Infof("%v HTTP %v, %v", arh.prefix, http.StatusBadRequest, err)
		klog.V(4).Info(arh.prefix, string(httpBody))
		response.WriteHeader(http.StatusBadRequest)
		return
	} else if admissionReview.Request == nil {
		klog.V(2).Infof("%v HTTP %v, empty AdmissionReview.Request", arh.prefix, http.StatusBadRequest)
		klog.V(4).Info(arh.prefix, admissionReview.String())
		response.WriteHeader(http.StatusBadRequest)
		return
	}

	admissionReviewResponse, err := arh.admissionFunction(admissionReview.Request)
	if err != nil {
		klog.V(2).Infof("%v HTTP %v, error %v", arh.prefix, http.StatusInternalServerError, err)
		klog.V(4).Info(arh.prefix, admissionReview.String())
		response.WriteHeader(http.StatusInternalServerError)
		return
	}
	klog.V(4).Infof("%v %v", arh.prefix, admissionReviewResponse.String())

	admissionReview.Response = admissionReviewResponse
	if admissionReview.Response.UID == "" {
		admissionReview.Response.UID = admissionReview.Request.UID
	}

	responseBytes, err := json.Marshal(&admissionReview)
	if err != nil {
		klog.V(2).Infof("%v HTTP %v, error %v", arh.prefix, http.StatusInternalServerError, err)
		klog.V(4).Info(arh.prefix, admissionReview.String())
		response.WriteHeader(http.StatusInternalServerError)
		return
	}

	response.WriteHeader(http.StatusOK)
	if _, err = response.Write(responseBytes); err != nil {
		klog.V(2).Infof("%v HTTP %v, error %v", arh.prefix, http.StatusOK, err)
		klog.V(4).Info(arh.prefix, admissionReview.String())
	}
	klog.V(2).Infof("%v HTTP %v, %+v", arh.prefix, http.StatusOK, struct {
		namespace string
		name      string
		kind      metav1.GroupVersionKind
		allowed   bool
		result    *metav1.Status
		user      authv1.UserInfo
	}{
		namespace: admissionReview.Request.Namespace,
		name:      findName(admissionReview.Request),
		kind:      admissionReview.Request.Kind,
		allowed:   admissionReview.Response.Allowed,
		result:    admissionReview.Response.Result,
		user:      admissionReview.Request.UserInfo,
	})
	klog.V(5).Info(arh.prefix, string(responseBytes))
}

func findName(request *v1beta1.AdmissionRequest) string {
	switch request.Kind.Kind {
	case "Deployment":
		var deployment appsv1.Deployment
		if err := json.Unmarshal(request.Object.Raw, &deployment); err != nil {
			return ""
		}
		return deployment.Name
	case "Pod":
		var pod v1.Pod
		if err := json.Unmarshal(request.Object.Raw, &pod); err != nil {
			return ""
		}
		return pod.Name
	default:
		return ""
	}
}
