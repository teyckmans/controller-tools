package swagger

import (
	"fmt"
	"sigs.k8s.io/controller-tools/pkg/crd"
	"strings"

	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/go-openapi/spec"
)

type ActionsContext struct {
	groupInCamelCase string
	contentTypes     []string
	schemes          []string
	crdRaw           *apiext.CustomResourceDefinition
	swagger          *spec.Swagger
	packageMapper    *PackageMapper
	parser           *crd.Parser
}

func crdActions(ctx *ActionsContext) error {
	crdRaw := ctx.crdRaw

	for i := range crdRaw.Spec.Versions {
		typeIdent := typeIdentFor(crdRaw.Spec.Group, crdRaw.Spec.Versions[i].Name, crdRaw.Spec.Names.Kind, ctx.parser)
		err := crdVersionActions(ctx, crdRaw.Spec.Versions[i], typeIdent)
		if err != nil {
			return err
		}
	}

	return nil
}

func typeIdentFor(group string, version string, kind string, parser *crd.Parser) *crd.TypeIdent {
	for typeIdent, gvk := range parser.CrdTypes {
		if gvk.Group == group && gvk.Version == version && gvk.Kind == kind {
			return &typeIdent
		}
	}
	return nil
}

func crdVersionActions(ctx *ActionsContext, version apiext.CustomResourceDefinitionVersion, typeIdent *crd.TypeIdent) error {
	crdRaw := ctx.crdRaw
	namespaced := crdRaw.Spec.Scope == apiext.NamespaceScoped

	namespacePathPart := ""
	if namespaced {
		namespacePathPart = "/namespaces/{namespace}"
	}

	crdURLBase := fmt.Sprintf("/apis/%s/%s%s/%s", crdRaw.Spec.Group, version.Name, namespacePathPart, crdRaw.Spec.Names.Plural)

	if namespaced {
		clusterCrdURLBase := fmt.Sprintf("/apis/%s/%s/%s", crdRaw.Spec.Group, version.Name, crdRaw.Spec.Names.Plural)
		clusterReadAction(ctx, version, clusterCrdURLBase, typeIdent)
	}

	pluralActions(ctx, version, crdURLBase, namespaced, typeIdent)
	singularActions(ctx, version, crdURLBase, namespaced, typeIdent)

	return nil
}

func clusterReadAction(ctx *ActionsContext, version apiext.CustomResourceDefinitionVersion, crdURLPath string, typeIdent *crd.TypeIdent) {
	crdRaw := ctx.crdRaw
	swaggerSpec := ctx.swagger

	crdVersionName := version.Name

	kind := crdRaw.Spec.Names.Kind

	swaggerSpec.SwaggerProps.Paths.Paths[crdURLPath] = spec.PathItem{
		PathItemProps: spec.PathItemProps{
			Get:        namespacedClusterGetAction(ctx, crdVersionName, kind, typeIdent),
			Parameters: collectionOperationParameters(true),
		},
	}
}

func singularActions(ctx *ActionsContext, version apiext.CustomResourceDefinitionVersion, crdURLBase string, namespaced bool, typeIdent *crd.TypeIdent) {
	crdRaw := ctx.crdRaw
	swaggerSpec := ctx.swagger

	crdVersionName := version.Name

	crdURLPath := fmt.Sprintf("%s/{name}", crdURLBase)

	kind := crdRaw.Spec.Names.Kind
	mappedTypeIndent := ctx.packageMapper.mapTypeIdent(*typeIdent)
	kindRef := fmt.Sprintf("#/definitions/%s", mappedTypeIndent)

	nameParameter := simpleParameter(
		"string",
		"name",
		fmt.Sprintf("name of the %s", crdRaw.Spec.Names.Kind),
		"path",
		true,
		true)

	var resourceParameters []spec.Parameter

	if namespaced {
		resourceParameters = []spec.Parameter{
			nameParameter,
			namespaceParameter(),
			prettyParameter(),
		}
	} else {
		resourceParameters = []spec.Parameter{
			nameParameter,
			prettyParameter(),
		}
	}

	swaggerSpec.SwaggerProps.Paths.Paths[crdURLPath] = spec.PathItem{
		PathItemProps: spec.PathItemProps{
			Get:        getAction(ctx, crdVersionName, kind, kindRef, "", namespaced),
			Put:        putAction(ctx, crdVersionName, kind, kindRef, "", namespaced),
			Delete:     deleteAction(ctx, crdVersionName, kind, "", namespaced),
			Patch:      patchAction(ctx, crdVersionName, kind, kindRef, "", namespaced),
			Parameters: resourceParameters,
		},
	}

	if version.Subresources != nil && version.Subresources.Status != nil {
		crdStatusURLPath := crdURLPath + "/status"
		swaggerSpec.SwaggerProps.Paths.Paths[crdStatusURLPath] = spec.PathItem{
			PathItemProps: spec.PathItemProps{
				Get:        getAction(ctx, crdVersionName, kind, kindRef, "Status", namespaced),
				Put:        putAction(ctx, crdVersionName, kind, kindRef, "Status", namespaced),
				Patch:      patchAction(ctx, crdVersionName, kind, kindRef, "Status", namespaced),
				Parameters: resourceParameters,
			},
		}
	}

	// TODO(teyckmans) support scale sub resource
}

func patchAction(ctx *ActionsContext, crdVersionName string, kind string, kindRef string, status string, namespaced bool) *spec.Operation {
	action := "patch"

	responses := operationResponses()
	responses[200] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "OK",
			Schema:      spec.RefSchema(kindRef),
		},
	}

	return &spec.Operation{
		OperationProps: spec.OperationProps{
			Description: singularOperationDescription("partially update%s the specified %s", kind, status),
			Consumes: []string{
				"application/json-patch+json",
				"application/merge-patch+json",
				"application/apply-patch+yaml",
			},
			Produces: ctx.contentTypes,
			Schemes:  ctx.schemes,
			Tags:     operationTags(ctx, crdVersionName),
			ID:       operationID(ctx, crdVersionName, action, "", status, namespaced),
			Parameters: []spec.Parameter{
				bodyParameter("#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.Patch", true),
				dryRunParameter(),
				fieldManagerParameter(),
			},
			Responses: &spec.Responses{
				ResponsesProps: spec.ResponsesProps{
					StatusCodeResponses: responses,
				},
			},
		},
		VendorExtensible: spec.VendorExtensible{
			Extensions: operationExtensions(ctx, crdVersionName, action),
		},
	}
}

func deleteAction(ctx *ActionsContext, crdVersionName string, kind string, status string, namespaced bool) *spec.Operation {
	action := "delete"

	statusRef := "#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.Status"

	responses := operationResponses()
	responses[200] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "OK",
			Schema:      spec.RefSchema(statusRef),
		},
	}
	responses[202] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "Accepted",
			Schema:      spec.RefSchema(statusRef),
		},
	}

	return &spec.Operation{
		OperationProps: spec.OperationProps{
			Description: fmt.Sprintf("delete a %s", kind),
			Consumes:    ctx.contentTypes,
			Produces:    ctx.contentTypes,
			Schemes:     ctx.schemes,
			Tags:        operationTags(ctx, crdVersionName),
			ID:          operationID(ctx, crdVersionName, action, "", status, namespaced),
			Parameters: []spec.Parameter{
				bodyParameter("#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.DeleteOptions", false),
				dryRunParameter(),
				integerQueryParameter(
					"gracePeriodSeconds",
					"The duration in seconds before the object should be deleted. Value must be non-negative integer. The value zero indicates delete immediately. If this value is nil, the default grace period for the specified type will be used. Defaults to a per object value if not specified. zero means delete immediately.",
					true),
				booleanQueryParameter(
					"orphanDependents",
					"Deprecated: please use the PropagationPolicy, this field will be deprecated in 1.7. Should the dependent objects be orphaned. If true/false, the \"orphan\" finalizer will be added to/removed from the object's finalizers list. Either this field or PropagationPolicy may be set, but not both.",
					true),
				stringQueryParameter("propagationPolicy", "Whether and how garbage collection will be performed. Either this field or OrphanDependents may be set, but not both. The default policy is decided by the existing finalizer set in the metadata.finalizers and the resource-specific default policy. Acceptable values are: 'Orphan' - orphan the dependents; 'Background' - allow the garbage collector to delete the dependents in the background; 'Foreground' - a cascading policy that deletes all dependents in the foreground."),
			},
			Responses: &spec.Responses{
				ResponsesProps: spec.ResponsesProps{
					StatusCodeResponses: responses,
				},
			},
		},
		VendorExtensible: spec.VendorExtensible{
			Extensions: operationExtensions(ctx, crdVersionName, action),
		},
	}
}

func putAction(ctx *ActionsContext, crdVersionName string, kind string, kindRef string, status string, namespaced bool) *spec.Operation {
	action := "put"

	responses := operationResponses()
	responses[200] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "OK",
			Schema:      spec.RefSchema(kindRef),
		},
	}
	responses[201] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "Created",
			Schema:      spec.RefSchema(kindRef),
		},
	}

	return &spec.Operation{
		OperationProps: spec.OperationProps{
			Description: singularOperationDescription("replace%s the specified %s", kind, status),
			Consumes:    ctx.contentTypes,
			Produces:    ctx.contentTypes,
			Schemes:     ctx.schemes,
			Tags:        operationTags(ctx, crdVersionName),
			ID:          operationID(ctx, crdVersionName, "replace", "", status, namespaced),
			Parameters: []spec.Parameter{
				bodyParameter(kindRef, true),
				dryRunParameter(),
				fieldManagerParameter(),
			},
			Responses: &spec.Responses{
				ResponsesProps: spec.ResponsesProps{
					StatusCodeResponses: responses,
				},
			},
		},
		VendorExtensible: spec.VendorExtensible{
			Extensions: operationExtensions(ctx, crdVersionName, action),
		},
	}
}

func namespacedClusterGetAction(ctx *ActionsContext, crdVersionName string, kind string, typeIdent *crd.TypeIdent) *spec.Operation {
	action := "list"

	mappedTypeIdent := ctx.packageMapper.mapTypeIdent(*typeIdent)
	listKindRef := fmt.Sprintf("#/definitions/%s", mappedTypeIdent)

	responses := operationResponses()
	responses[200] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "OK",
			Schema:      spec.RefSchema(listKindRef),
		},
	}

	return &spec.Operation{
		OperationProps: spec.OperationProps{
			Description: fmt.Sprintf("%s objects of kind %s", action, kind),
			Consumes:    ctx.contentTypes,
			Produces:    ctx.contentTypes,
			Schemes:     ctx.schemes,
			Tags:        operationTags(ctx, crdVersionName),
			ID:          clusterOperationID(ctx, crdVersionName, action, "", "", true),
			Parameters:  []spec.Parameter{},
			Responses: &spec.Responses{
				ResponsesProps: spec.ResponsesProps{
					StatusCodeResponses: responses,
				},
			},
		},
		VendorExtensible: spec.VendorExtensible{
			Extensions: operationExtensions(ctx, crdVersionName, action),
		},
	}
}

func getAction(ctx *ActionsContext, crdVersionName string, kind string, kindRef string, status string, namespaced bool) *spec.Operation {
	action := "get"

	responses := operationResponses()
	responses[200] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "OK",
			Schema:      spec.RefSchema(kindRef),
		},
	}

	return &spec.Operation{
		OperationProps: spec.OperationProps{
			Description: singularOperationDescription("read%s the specified %s", kind, status),
			Consumes:    ctx.contentTypes,
			Produces:    ctx.contentTypes,
			Schemes:     ctx.schemes,
			Tags:        operationTags(ctx, crdVersionName),
			ID:          operationID(ctx, crdVersionName, "read", "", status, namespaced),
			Parameters: []spec.Parameter{
				stringQueryParameter("resourceVersion", "When specified: - if unset, then the result is returned from remote storage based on quorum-read flag; - if it's 0, then we simply return what we currently have in cache, no guarantee; - if set to non zero, then the result is at least as fresh as given rv."),
			},
			Responses: &spec.Responses{
				ResponsesProps: spec.ResponsesProps{
					StatusCodeResponses: responses,
				},
			},
		},
		VendorExtensible: spec.VendorExtensible{
			Extensions: operationExtensions(ctx, crdVersionName, action),
		},
	}
}

func pluralActions(ctx *ActionsContext, version apiext.CustomResourceDefinitionVersion, crdURLPath string, namespaced bool, typeIdent *crd.TypeIdent) {
	crdRaw := ctx.crdRaw
	swaggerSpec := ctx.swagger

	crdVersionName := version.Name

	kind := crdRaw.Spec.Names.Kind
	mappedType := ctx.packageMapper.mapTypeIdent(*typeIdent)
	kindRef := fmt.Sprintf("#/definitions/%s", mappedType)
	mappedListType := ctx.packageMapper.mapPackageAndTypeName(typeIdent.Package.PkgPath, crdRaw.Spec.Names.ListKind)
	listKindRef := fmt.Sprintf("#/definitions/%s", mappedListType)

	var resourceParameters []spec.Parameter

	if namespaced {
		resourceParameters = []spec.Parameter{
			namespaceParameter(),
			prettyParameter(),
		}
	} else {
		resourceParameters = []spec.Parameter{
			prettyParameter(),
		}
	}

	swaggerSpec.SwaggerProps.Paths.Paths[crdURLPath] = spec.PathItem{
		PathItemProps: spec.PathItemProps{
			Get:        listAction(ctx, crdVersionName, kind, listKindRef, namespaced),
			Post:       postAction(ctx, crdVersionName, kind, kindRef, namespaced),
			Delete:     deleteCollectionAction(ctx, crdVersionName, kind, namespaced),
			Parameters: resourceParameters,
		},
	}
}

func deleteCollectionAction(ctx *ActionsContext, crdVersionName string, kind string, namespaced bool) *spec.Operation {
	action := "deletecollection"

	responses := operationResponses()

	statusRef := "#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.Status"

	responses[200] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "OK",
			Schema:      spec.RefSchema(statusRef),
		},
	}

	return &spec.Operation{
		OperationProps: spec.OperationProps{
			Description: fmt.Sprintf("delete collection of %s", kind),
			Consumes:    ctx.contentTypes,
			Produces:    ctx.contentTypes,
			Schemes:     ctx.schemes,
			Tags:        operationTags(ctx, crdVersionName),
			ID:          operationID(ctx, crdVersionName, "delete", "Collection", "", namespaced),
			Parameters:  collectionOperationParameters(false),
			Responses: &spec.Responses{
				ResponsesProps: spec.ResponsesProps{
					StatusCodeResponses: responses,
				},
			},
		},
		VendorExtensible: spec.VendorExtensible{
			Extensions: operationExtensions(ctx, crdVersionName, action),
		},
	}
}

func postAction(ctx *ActionsContext, crdVersionName string, kind string, kindRef string, namespaced bool) *spec.Operation {
	action := "post"

	responses := operationResponses()
	responses[200] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "OK",
			Schema:      spec.RefSchema(kindRef),
		},
	}
	responses[201] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "Created",
			Schema:      spec.RefSchema(kindRef),
		},
	}
	responses[202] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "Accepted",
			Schema:      spec.RefSchema(kindRef),
		},
	}

	return &spec.Operation{
		OperationProps: spec.OperationProps{
			Description: fmt.Sprintf("create a %s", kind),
			Consumes:    ctx.contentTypes,
			Produces:    ctx.contentTypes,
			Schemes:     ctx.schemes,
			Tags:        operationTags(ctx, crdVersionName),
			ID:          operationID(ctx, crdVersionName, "create", "", "", namespaced),
			Parameters: []spec.Parameter{
				bodyParameter(kindRef, true),
				dryRunParameter(),
				fieldManagerParameter(),
			},
			Responses: &spec.Responses{
				ResponsesProps: spec.ResponsesProps{
					StatusCodeResponses: responses,
				},
			},
		},
		VendorExtensible: spec.VendorExtensible{
			Extensions: operationExtensions(ctx, crdVersionName, action),
		},
	}
}

func listAction(ctx *ActionsContext, crdVersionName string, kind string, listKindRef string, namespaced bool) *spec.Operation {
	action := "list"

	responses := operationResponses()
	responses[200] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "OK",
			Schema:      spec.RefSchema(listKindRef),
		},
	}

	return &spec.Operation{
		OperationProps: spec.OperationProps{
			Description: fmt.Sprintf("%s objects of kind %s", action, kind),
			Consumes:    ctx.contentTypes,
			Produces:    ctx.contentTypes,
			Schemes:     ctx.schemes,
			Tags:        operationTags(ctx, crdVersionName),
			ID:          operationID(ctx, crdVersionName, action, "", "", namespaced),
			Parameters:  collectionOperationParameters(false),
			Responses: &spec.Responses{
				ResponsesProps: spec.ResponsesProps{
					StatusCodeResponses: responses,
				},
			},
		},
		VendorExtensible: spec.VendorExtensible{
			Extensions: operationExtensions(ctx, crdVersionName, action),
		},
	}
}

func collectionOperationParameters(prettyParameterNeeded bool) []spec.Parameter {
	var parameters []spec.Parameter

	parameters = append(parameters, booleanQueryParameter(
		"allowWatchBookmarks",
		"allowWatchBookmarks requests watch events with type \"BOOKMARK\". Servers that do not implement bookmarks may ignore this flag and bookmarks are sent at the server's discretion. Clients should not assume bookmarks are returned at any specific interval, nor may they assume the server will send any BOOKMARK event during a session. If this is not a watch, this field is ignored. If the feature gate WatchBookmarks is not enabled in apiserver, this field is ignored.",
		true))
	parameters = append(parameters, stringQueryParameter("continue", "The continue option should be set when retrieving more results from the server. Since this value is server defined, clients may only use the continue value from a previous query result with identical query parameters (except for the value of continue) and the server may reject a continue value it does not recognize. If the specified continue value is no longer valid whether due to expiration (generally five to fifteen minutes) or a configuration change on the server, the server will respond with a 410 ResourceExpired error together with a continue token. If the client needs a consistent list, it must restart their list without the continue field. Otherwise, the client may send another list request with the token received with the 410 error, the server will respond with a list starting from the next key, but from the latest snapshot, which is inconsistent from the previous list results - objects that are created, modified, or deleted after the first list request will be included in the response, as long as their keys are after the \"next key\".\n\nThis field is not supported when watch is true. Clients may start a watch from the last resourceVersion value returned by the server and not miss any modifications."))
	parameters = append(parameters, stringQueryParameter("fieldSelector", "A selector to restrict the list of returned objects by their fields. Defaults to everything."))
	parameters = append(parameters, stringQueryParameter("labelSelector", "A selector to restrict the list of returned objects by their labels. Defaults to everything."))
	parameters = append(parameters, integerQueryParameter(
		"limit",
		"limit is a maximum number of responses to return for a list call. If more items exist, the server will set the `continue` field on the list metadata to a value that can be used with the same initial query to retrieve the next set of results. Setting a limit may return fewer than the requested amount of items (up to zero items) in the event all requested objects are filtered out and clients should only use the presence of the continue field to determine whether more results are available. Servers may choose not to support the limit argument and will return all of the available results. If limit is specified and the continue field is empty, clients may assume that no more results are available. This field is not supported if watch is true.\n\nThe server guarantees that the objects returned when using continue will be identical to issuing a single list call without a limit - that is, no objects created, modified, or deleted after the first request is issued will be included in any subsequent continued requests. This is sometimes referred to as a consistent snapshot, and ensures that a client that is using limit to receive smaller chunks of a very large result can ensure they see all possible objects. If objects are updated during a chunked list the version of the object that was present at the time the first list result was calculated is returned.",
		true))
	if prettyParameterNeeded {
		parameters = append(parameters, prettyParameter())
	}
	parameters = append(parameters, stringQueryParameter("resourceVersion", "When specified with a watch call, shows changes that occur after that particular version of a resource. Defaults to changes from the beginning of history. When specified for list: - if unset, then the result is returned from remote storage based on quorum-read flag; - if it's 0, then we simply return what we currently have in cache, no guarantee; - if set to non zero, then the result is at least as fresh as given rv."))
	parameters = append(parameters, integerQueryParameter(
		"timeoutSeconds",
		"Timeout for the list/watch call. This limits the duration of the call, regardless of any activity or inactivity.",
		true))
	parameters = append(parameters, booleanQueryParameter(
		"watch",
		"Watch for changes to the described resources and return them as a stream of add, update, and remove notifications. Specify resourceVersion.",
		true))

	return parameters
}

func integerQueryParameter(name string, description string, uniqueItems bool) spec.Parameter {
	return optionalSimpleParameter("integer", name, description, uniqueItems)
}

func booleanQueryParameter(name string, description string, uniqueItems bool) spec.Parameter {
	return optionalSimpleParameter("boolean", name, description, uniqueItems)
}

func stringQueryParameter(name string, description string) spec.Parameter {
	return optionalSimpleParameter("string", name, description, true)
}

func optionalSimpleParameter(simpleType string, name string, desc string, uniqueItems bool) spec.Parameter {
	return simpleParameter(simpleType, name, desc, "query", uniqueItems, false)
}

func simpleParameter(simpleType string, name string, desc string, in string, uniqueItems bool, required bool) spec.Parameter {
	return spec.Parameter{
		CommonValidations: spec.CommonValidations{
			UniqueItems: uniqueItems,
		},
		ParamProps: spec.ParamProps{
			Description: desc,
			Name:        name,
			In:          in,
			Required:    required,
		},
		SimpleSchema: spec.SimpleSchema{
			Type: simpleType,
		},
	}
}

func bodyParameter(definitionName string, required bool) spec.Parameter {
	var param spec.Parameter

	param = spec.Parameter{
		ParamProps: spec.ParamProps{
			Name:     "body",
			In:       "body",
			Schema:   spec.RefSchema(definitionName),
			Required: required,
		},
	}

	return param
}

func singularOperationDescription(format string, subject string, status string) string {
	statusOf := ""
	if status != "" {
		statusOf = " status of"
	}

	return fmt.Sprintf(format, statusOf, subject)
}

func operationTags(ctx *ActionsContext, crdVersionName string) []string {
	return []string{fmt.Sprintf("%s_%s", ctx.groupInCamelCase, crdVersionName)}
}

func operationID(ctx *ActionsContext, crdVersionName string, action string, collection string, status string, namespaced bool) string {
	crdRaw := ctx.crdRaw

	namespacedPart := "Namespaced"
	if !namespaced {
		namespacedPart = ""
	}

	return fmt.Sprintf("%s%s%s%s%s%s%s", action, strings.Title(ctx.groupInCamelCase), strings.Title(crdVersionName), collection, namespacedPart, crdRaw.Spec.Names.Kind, status)
}

func clusterOperationID(ctx *ActionsContext, crdVersionName string, action string, collection string, status string, namespaced bool) string {
	crdRaw := ctx.crdRaw

	namespacedPart := "ForAllNamespaces"
	if !namespaced {
		namespacedPart = ""
	}

	return fmt.Sprintf("%s%s%s%s%s%s%s", action, strings.Title(ctx.groupInCamelCase), strings.Title(crdVersionName), collection, crdRaw.Spec.Names.Kind, status, namespacedPart)
}

func operationResponses() map[int]spec.Response {
	responses := map[int]spec.Response{}

	responses[401] = spec.Response{
		ResponseProps: spec.ResponseProps{
			Description: "Unauthorized",
		},
	}

	return responses
}

func operationExtensions(ctx *ActionsContext, crdVersionName string, action string) map[string]interface{} {
	crdRaw := ctx.crdRaw

	extensions := map[string]interface{}{}
	extensions["x-kubernetes-action"] = action

	groupVersionKind := groupVersionKind(crdRaw, crdVersionName)

	extensions["x-kubernetes-group-version-kind"] = groupVersionKind

	return extensions
}

func prettyParameter() spec.Parameter {
	return optionalSimpleParameter("string", "pretty", "If 'true', then the output is pretty printed.", true)
}

func namespaceParameter() spec.Parameter {
	return simpleParameter(
		"string",
		"namespace",
		"object name and auth scope, such as for teams and projects",
		"path",
		true,
		true)
}

func dryRunParameter() spec.Parameter {
	return stringQueryParameter("dryRun", "When present, indicates that modifications should not be persisted. An invalid or unrecognized dryRun directive will result in an error response and no further processing of the request. Valid values are: - All: all dry run stages will be processed")
}

func fieldManagerParameter() spec.Parameter {
	return stringQueryParameter("fieldManager", "fieldManager is a name associated with the actor or entity that is making these changes. The value must be less than or 128 characters long, and only contain printable characters, as defined by https://golang.org/pkg/unicode/#IsPrint.")
}
