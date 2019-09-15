package image

import (
	"fmt"
	"k8s.io/api/admission/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
	"strings"
	"time"
)

func ImageVersionIsNotLatest(request *v1beta1.AdmissionRequest) (*v1beta1.AdmissionResponse, error) {
	switch request.Kind.Kind {
	case "Deployment":
		var deployment appsv1.Deployment
		if err := json.Unmarshal(request.Object.Raw, &deployment); err != nil {
			return &v1beta1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Message: err.Error(),
					Reason:  metav1.StatusReasonBadRequest,
				},
			}, nil
		}
		return verifyImageVersion(&deployment.Spec.Template.Spec)
	case "Pod":
		var pod v1.Pod
		if err := json.Unmarshal(request.Object.Raw, &pod); err != nil {
			return &v1beta1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Message: err.Error(),
					Reason:  metav1.StatusReasonBadRequest,
				},
			}, nil
		}
		return verifyImageVersion(&pod.Spec)
	default:
		return &v1beta1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("kind not supported %v", request.RequestKind.String()),
				Reason:  metav1.StatusReasonNotAcceptable,
			},
		}, nil
	}
}

func verifyImageVersion(spec *v1.PodSpec) (*v1beta1.AdmissionResponse, error) {
	var failedContainers []string
	for _, container := range append(spec.Containers, spec.InitContainers...) {
		if !strings.Contains(container.Image, ":") || "latest" == strings.ToLower(strings.Split(container.Image, ":")[1]) {
			failedContainers = append(failedContainers, container.Name)
		}
	}

	if len(failedContainers) > 0 {
		return &v1beta1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("image version 'latest' is not allowed, but was found in containers: %v", strings.Join(failedContainers, ", ")),
				Reason:  metav1.StatusReasonNotAcceptable,
			},
		}, nil
	}

	return &v1beta1.AdmissionResponse{
		Allowed: true,
		AuditAnnotations: map[string]string{
			"k8s-ac/validators/image-not-latest": fmt.Sprintf("validated at %v", time.Now()),
		},
	}, nil
}
