package swagger

import (
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func groupVersionKind(crdRaw *apiext.CustomResourceDefinition, crdVersionName string) map[string]string {
	groupVersionKind := map[string]string{}
	groupVersionKind["group"] = crdRaw.Spec.Group
	groupVersionKind["kind"] = crdRaw.Spec.Names.Kind
	groupVersionKind["version"] = crdVersionName
	return groupVersionKind
}
