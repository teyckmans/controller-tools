package swagger

import (
	"go/ast"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-tools/pkg/loader"
	"strings"

	"sigs.k8s.io/controller-tools/pkg/crd"
	crdmarkers "sigs.k8s.io/controller-tools/pkg/crd/markers"
	"sigs.k8s.io/controller-tools/pkg/genall"
	"sigs.k8s.io/controller-tools/pkg/markers"

	"github.com/go-openapi/spec"
)

type Generator struct {
	CrdRootPackage string `marker:",required"`
	// AllowDangerousTypes allows types which are usually omitted from CRD generation
	// because they are not recommended.
	//
	// Currently the following additional types are allowed when this is true:
	// float32
	// float64
	//
	// Left unspecified, the default is false
	AllowDangerousTypes *bool `marker:",optional"`

	// MaxDescLen specifies the maximum description length for fields in CRD's OpenAPI schema.
	//
	// 0 indicates drop the description for all fields completely.
	// n indicates limit the description to at most n characters and truncate the description to
	// closest sentence boundary if it exceeds n characters.
	MaxDescLen *int `marker:",optional"`
}

func (Generator) CheckFilter() loader.NodeFilter {
	return filterTypesForSwagger
}

func (Generator) RegisterMarkers(into *markers.Registry) error {
	return crdmarkers.Register(into)
}
func (g Generator) Generate(ctx *genall.GenerationContext) error {
	parser := &crd.Parser{
		Collector: ctx.Collector,
		Checker:   ctx.Checker,
		// Perform defaulting here to avoid ambiguity later
		AllowDangerousTypes: g.AllowDangerousTypes != nil && *g.AllowDangerousTypes == true,
		Roots:               ctx.Roots,
	}

	crd.AddKnownTypes(parser)
	// TODO add extension point where CRD authors can define custom definitions for types. (e.g. ArrayOrString for tektoncd pipelines).
	for _, root := range ctx.Roots {
		parser.NeedPackage(root)
	}

	metav1Pkg := crd.FindMetav1(ctx.Roots)
	if metav1Pkg == nil {
		// no objects in the roots, since nothing imported metav1
		return nil
	}

	kubeKinds := crd.FindKubeKinds(parser, metav1Pkg)
	if len(kubeKinds) == 0 {
		// no objects in the roots
		return nil
	}

	filteredKubeKinds := make(map[schema.GroupKind]crd.TypeIdent)
	for groupKind, typeIdent := range kubeKinds {
		if strings.HasPrefix(typeIdent.Package.PkgPath, g.CrdRootPackage) {
			filteredKubeKinds[groupKind] = typeIdent
		}
	}

	// build string with list of groups for title of the swagger file.
	uniqueGroups := make(map[string]struct{}) // fake set
	for groupKind := range filteredKubeKinds {
		uniqueGroups[groupKind.Group] = struct{}{}
	}
	groupsInfo := ""
	var includedGroups []string
	for uniqueGroup := range uniqueGroups {
		includedGroups = append(includedGroups, uniqueGroup)
		if len(groupsInfo) == 0 {
			groupsInfo = uniqueGroup
		} else {
			groupsInfo = groupsInfo + ", " + uniqueGroup
		}
	}

	println("groups: " + groupsInfo)

	swaggerSpec := spec.Swagger{
		SwaggerProps: spec.SwaggerProps{
			Swagger: "2.0",
			Info: &spec.Info{
				InfoProps: spec.InfoProps{
					Title:   "Kubernetes (" + groupsInfo + ")",
					Version: "v1.18.2",
				},
			},
			Paths: &spec.Paths{
				Paths: make(map[string]spec.PathItem),
			},
			Definitions: spec.Definitions{},
		},
	}
	contentTypes := []string{"application/json", "application/yaml"}
	schemes := []string{"https"}

	for groupKind := range filteredKubeKinds {
		parser.NeedCRDFor(groupKind, g.MaxDescLen)
	}

	packageMapper := PackageMapper{
		includedGroups: includedGroups,
		crdTypes:       parser.CrdTypes,
		crdRootPackage: g.CrdRootPackage,
	}
	packageMapper.init()

	err := addDefinitions(&DefinitionsContext{
		swaggerSpec:    &swaggerSpec,
		parser:         parser,
		packageMapper:  &packageMapper,
		roots:          ctx.Roots,
		crdRootPackage: g.CrdRootPackage,
	})
	if err != nil {
		return err
	}

	for groupKind := range filteredKubeKinds {
		crdRaw := parser.CustomResourceDefinitions[groupKind]

		groupInCamelCases := ""
		groupSplits := strings.Split(groupKind.Group, ".")
		for i, groupSplit := range groupSplits {
			if i == 0 {
				groupInCamelCases = groupSplit
			} else {
				groupInCamelCases = groupInCamelCases + strings.Title(groupSplit)
			}
		}

		actionsContext := ActionsContext{
			groupInCamelCase: groupInCamelCases,
			contentTypes:     contentTypes,
			schemes:          schemes,
			crdRaw:           &crdRaw,
			swagger:          &swaggerSpec,
			packageMapper:    &packageMapper,
			parser:           parser,
		}
		err := crdActions(&actionsContext)
		if err != nil {
			return err
		}

	}

	if err := ctx.WriteSwagger("swagger.json", swaggerSpec); err != nil {
		return err
	}

	return nil
}

func filterTypesForSwagger(node ast.Node) bool {
	return true
}
