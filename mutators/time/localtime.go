package time

import (
	"fmt"
	"k8s.io/api/admission/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
	"time"
)

type patch struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

const localtimeVolumeName string = "auto-localtime"
const localtimePath string = "/etc/localtime"

func InjectNodeLocaltime(request *v1beta1.AdmissionRequest) (*v1beta1.AdmissionResponse, error) {
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
		return injectIntoPodSpec(&deployment.Spec.Template.Spec, "/spec/template")
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
		return injectIntoPodSpec(&pod.Spec, "")
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

func localtimeVolumeExists(volumes []v1.Volume) bool {
	for _, volume := range volumes {
		if volume.HostPath != nil && volume.Name == localtimeVolumeName && volume.HostPath.Path == localtimePath {
			return true
		}
	}
	return false
}

func localtimeVolumeMountExists(mounts []v1.VolumeMount) bool {
	for _, volumeMount := range mounts {
		if volumeMount.MountPath == localtimePath {
			return true
		}
	}
	return false
}

func injectIntoPodSpec(spec *v1.PodSpec, jsonPathPrefix string) (*v1beta1.AdmissionResponse, error) {
	var patches []*patch
	if !localtimeVolumeExists(spec.Volumes) {
		if len(spec.Volumes) == 0 {
			patches = append(patches, &patch{
				Op:    "add",
				Path:  fmt.Sprintf("%v/spec/volumes", jsonPathPrefix),
				Value: []v1.Volume{},
			})
		}
		patches = append(patches, &patch{
			Op:   "add",
			Path: fmt.Sprintf("%v/spec/volumes/-", jsonPathPrefix),
			Value: &v1.Volume{
				Name: localtimeVolumeName,
				VolumeSource: v1.VolumeSource{
					HostPath: &v1.HostPathVolumeSource{
						Path: localtimePath,
					},
				},
			},
		})
	}

	for i, container := range spec.Containers {
		if !localtimeVolumeMountExists(container.VolumeMounts) {
			if len(container.VolumeMounts) == 0 {
				patches = append(patches, &patch{
					Op:    "add",
					Path:  fmt.Sprintf("%v/spec/containers/%v/volumeMounts", jsonPathPrefix, i),
					Value: []v1.VolumeMount{},
				})
			}
			patches = append(patches, &patch{
				Op:   "add",
				Path: fmt.Sprintf("%v/spec/containers/%v/volumeMounts/-", jsonPathPrefix, i),
				Value: &v1.VolumeMount{
					Name:      localtimeVolumeName,
					MountPath: localtimePath,
				},
			})
		}
	}
	for i, container := range spec.InitContainers {
		if !localtimeVolumeMountExists(container.VolumeMounts) {
			if len(container.VolumeMounts) == 0 {
				patches = append(patches, &patch{
					Op:    "add",
					Path:  fmt.Sprintf("%v/spec/initContainers/%v/volumeMounts", jsonPathPrefix, i),
					Value: []v1.VolumeMount{},
				})
			}
			patches = append(patches, &patch{
				Op:   "add",
				Path: fmt.Sprintf("%v/spec/initContainers/%v/volumeMounts/-", jsonPathPrefix, i),
				Value: &v1.VolumeMount{
					Name:      localtimeVolumeName,
					MountPath: localtimePath,
				},
			})
		}
	}

	patchBytes, err := json.Marshal(patches)
	if err != nil {
		return nil, err
	}

	return &v1beta1.AdmissionResponse{
		Allowed: true,
		Patch:   patchBytes,
		AuditAnnotations: map[string]string{
			"k8s-ac/mutators/inject-localtime": fmt.Sprintf("injected %v at %v", localtimePath, time.Now()),
		},
	}, nil
}
