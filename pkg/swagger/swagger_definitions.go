package swagger

import (
	"strings"

	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-tools/pkg/crd"
	"sigs.k8s.io/controller-tools/pkg/loader"

	"github.com/go-openapi/spec"
)

type DefinitionsContext struct {
	swaggerSpec           *spec.Swagger
	parser                *crd.Parser
	packageMappings       map[string]string
	actionReferencedTypes map[string]bool
	roots                 []*loader.Package
	referencesToAdd       map[string]bool
}

/**
Embed simple types so we only remain with object types as elements in the swagger definitions map
as is the case for the vanilla k8s api swagger.
*/
func addDefinitions(ctx *DefinitionsContext) error {
	determinePackageMappings(ctx)

	addTypeDefinitions(ctx)

	return nil
}

func determinePackageMappings(ctx *DefinitionsContext) {
	parser := ctx.parser

	for typeIdent, gvk := range parser.CrdTypes {
		packageName := groupToPackageName(gvk.Group)

		mappedPackage := packageName + "." + gvk.Version

		var parentPackage string
		lastPackageSplitIndex := strings.LastIndex(typeIdent.Package.PkgPath, "/")
		if lastPackageSplitIndex != -1 {
			runes := []rune(typeIdent.Package.PkgPath)

			parentPackage = string(runes[0:lastPackageSplitIndex])
			parentPackage = strings.Replace(parentPackage, "/", "~1", -1)
			ctx.packageMappings[parentPackage] = packageName
		}

		versionPackageName := parentPackage + "~1" + typeIdent.Package.Name

		ctx.packageMappings[versionPackageName] = mappedPackage
	}
}

func isSimpleType(jsonSchema *apiext.JSONSchemaProps) bool {
	var isSimpleType bool

	if jsonSchema.Type == "" && jsonSchema.Ref != nil {
		// ref property
		isSimpleType = false
	} else if jsonSchema.Type == "object" && jsonSchema.AdditionalProperties != nil && jsonSchema.AdditionalProperties.Schema != nil {
		// map property
		isSimpleType = true
	} else if jsonSchema.Type != "object" {
		isSimpleType = true
	}

	return isSimpleType
}

func loadRef(ref string, ctx *DefinitionsContext) *crd.TypeIdent {
	goPackageName := goPackageName(ref)
	typeName := typeName(ref)

	loaderPackage := ctx.parser.LoaderPackage(goPackageName)
	typeIdent := crd.TypeIdent{Package: loaderPackage, Name: typeName}

	_, known := ctx.parser.Schemata[typeIdent]
	if !known {
		if strings.HasPrefix(goPackageName, "k8s.io") {
			return nil
		}

		ctx.parser.NeedSchemaForType(typeIdent)

		return &typeIdent
	}

	return &typeIdent
}

func goPackageName(jsonReference string) string {
	splitIndex := strings.LastIndex(jsonReference, "~0")
	packagePart := string([]rune(jsonReference)[0:splitIndex])
	packagePart = strings.Replace(packagePart, "#/definitions/", "", 1)
	packagePart = strings.ReplaceAll(packagePart, "~1", "/")
	return packagePart
}

func typeName(jsonReference string) string {
	splitIndex := strings.LastIndex(jsonReference, "~0")
	return string([]rune(jsonReference)[splitIndex+2:])
}

func addTypeDefinitions(ctx *DefinitionsContext) {
	parser := ctx.parser

	ctx.referencesToAdd = make(map[string]bool)

	for typeIdent := range parser.Schemata {
		jsonSchema := parser.Schemata[typeIdent]
		definitionRef := swaggerDefinitionRef(typeIdent, ctx)

		if _, ok := ctx.actionReferencedTypes[definitionRef]; ok {
			addTypeToSwaggerSpec(typeIdent, &jsonSchema, ctx.swaggerSpec, ctx)
		}
	}

	for {
		if len(ctx.referencesToAdd) == 0 {
			break
		}

		referencesToAdd := make(map[string]bool)
		// take a copy and reset the context map
		for referenceToAdd, flag := range ctx.referencesToAdd {
			referencesToAdd[referenceToAdd] = flag
		}
		ctx.referencesToAdd = make(map[string]bool)

		for referenceToAdd := range referencesToAdd {
			typeIdent := loadRef(referenceToAdd, ctx)
			if typeIdent != nil {
				jsonSchema := ctx.parser.Schemata[*typeIdent]
				addTypeToSwaggerSpec(*typeIdent, &jsonSchema, ctx.swaggerSpec, ctx)
			}
		}
	}
}

func swaggerDefinitionKey(typeIdent crd.TypeIdent, ctx *DefinitionsContext) string {
	jsonPackageName := strings.Replace(typeIdent.Package.PkgPath, "/", "~1", -1)

	return CleanUpReference(jsonPackageName+"~0"+typeIdent.Name, ctx.packageMappings)
}

func swaggerDefinitionRef(typeIdent crd.TypeIdent, ctx *DefinitionsContext) string {
	return "#/definitions/" + swaggerDefinitionKey(typeIdent, ctx)
}

func addTypeToSwaggerSpec(typeIdent crd.TypeIdent, jsonSchema *apiext.JSONSchemaProps, swaggerSpec *spec.Swagger, ctx *DefinitionsContext) {
	definitionKey := swaggerDefinitionKey(typeIdent, ctx)

	if strings.HasPrefix(definitionKey, "io.k8s") {
		// no need to include in crd-swagger file, included in api spec.
		return
	}

	if jsonSchema.Type == "object" {
		swaggerSpec.Definitions[definitionKey] = jsonSchemaObjectToSwaggerSchema(typeIdent, jsonSchema, ctx)
	} else if jsonSchema.Type == "" && jsonSchema.Ref != nil {
		swaggerSpec.Definitions[definitionKey] = jsonSchemaRefToSwaggerSchema(jsonSchema, ctx)
	} else {
		// TODO(teyckmans) if we get here there is something wrong with the embedding of simple references.
		println("!!SKIPPING!! " + definitionKey + " as it is not an object but of type: " + jsonSchema.Type + " with format " + jsonSchema.Format + " and ref " + *jsonSchema.Ref)
		return
	}
}

func jsonSchemaSimpleToSwaggerSchema(jsonSchema *apiext.JSONSchemaProps, ctx *DefinitionsContext) spec.Schema {
	var swaggerSchema spec.Schema

	if jsonSchema.Ref != nil {
		swaggerSchema = jsonSchemaRefToSwaggerSchema(jsonSchema, ctx)
	} else if jsonSchema.Type == "string" {
		swaggerSchema = jsonSchemaTypeWithFormatToSwaggerSchema(jsonSchema)
	} else if jsonSchema.Type == "boolean" {
		swaggerSchema = jsonSchemaTypeWithFormatToSwaggerSchema(jsonSchema)
	} else if jsonSchema.Type == "integer" {
		swaggerSchema = jsonSchemaTypeWithFormatToSwaggerSchema(jsonSchema)
	} else if jsonSchema.Type == "Any" {
		swaggerSchema = jsonSchemaTypeWithFormatToSwaggerSchema(jsonSchema)
	} else if jsonSchema.Type == "array" {
		var arrayItems spec.SchemaOrArray

		if jsonSchema.Items.Schema != nil {
			itemSwaggerSchema := jsonSchemaSimpleToSwaggerSchema(jsonSchema.Items.Schema, ctx)

			arrayItems = spec.SchemaOrArray{
				Schema: &itemSwaggerSchema,
			}
		}
		// jsonSchema.Items.JSONSchemas not supported

		swaggerSchema = spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: jsonSchema.Description,
				Type:        []string{"array"},
				Items:       &arrayItems,
				Required:    jsonSchema.Required,
			},
		}
	} else if jsonSchema.Type == "object" && jsonSchema.AdditionalProperties != nil && jsonSchema.AdditionalProperties.Schema != nil {
		// map property
		valueSchema := jsonSchemaSimpleToSwaggerSchema(jsonSchema.AdditionalProperties.Schema, ctx)

		swaggerSchema = spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: jsonSchema.Description,
				Type:        []string{"object"},
				AdditionalProperties: &spec.SchemaOrBool{
					Schema: &valueSchema,
				},
			},
		}
	} else {
		println("NEW jsonSchema.Type detected - default mapping used for type " + jsonSchema.Type)

		swaggerSchema = jsonSchemaTypeWithFormatToSwaggerSchema(jsonSchema)
	}

	return swaggerSchema
}

func followRefChain(refJSONSchema *apiext.JSONSchemaProps, ctx *DefinitionsContext) *apiext.JSONSchemaProps {
	refTypeIdent := loadRef(*refJSONSchema.Ref, ctx)

	if refTypeIdent == nil {
		return nil
	}

	nextRefJSONSchema := ctx.parser.Schemata[*refTypeIdent]

	if nextRefJSONSchema.Ref != nil {
		return followRefChain(&nextRefJSONSchema, ctx)
	}

	return &nextRefJSONSchema
}

func jsonSchemaRefToSwaggerSchema(jsonSchema *apiext.JSONSchemaProps, ctx *DefinitionsContext) spec.Schema {
	var swaggerSchema spec.Schema

	refJSONSchema := followRefChain(jsonSchema, ctx)
	embedRef := refJSONSchema != nil && isSimpleType(refJSONSchema)

	if embedRef {
		swaggerSchema = jsonSchemaSimpleToSwaggerSchema(refJSONSchema, ctx)
	} else {
		// object type reference || k8s.io type
		swaggerSchema = spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: jsonSchema.Description,
				Ref:         swaggerRef(*jsonSchema.Ref, ctx),
				Required:    jsonSchema.Required,
			},
		}
	}

	return swaggerSchema
}

func jsonSchemaTypeWithFormatToSwaggerSchema(jsonSchema *apiext.JSONSchemaProps) spec.Schema {
	return spec.Schema{
		SchemaProps: spec.SchemaProps{
			Description: jsonSchema.Description,
			Type:        []string{jsonSchema.Type},
			Format:      jsonSchema.Format,
			Required:    jsonSchema.Required,
		},
	}
}

func jsonSchemaObjectToSwaggerSchema(typeIdent crd.TypeIdent, jsonSchema *apiext.JSONSchemaProps, ctx *DefinitionsContext) spec.Schema {

	swaggerSchemaProps := spec.SchemaProps{
		Type:        []string{jsonSchema.Type},
		Description: jsonSchema.Description,
		Properties:  map[string]spec.Schema{},
	}
	swaggerSchema := spec.Schema{
		SchemaProps: swaggerSchemaProps,
	}

	for propertyName := range jsonSchema.Properties {
		property := jsonSchema.Properties[propertyName]
		addProperty(propertyName, &property, &swaggerSchemaProps, ctx)
	}
	allOf(jsonSchema, &swaggerSchemaProps, ctx)
	anyOf(jsonSchema, &swaggerSchemaProps, ctx)

	gvk, isCrd := ctx.parser.CrdTypes[typeIdent]
	if isCrd {
		// TODO(teyckmans): figure out how to handle multi gvk for same type, occurs in vanilla k8s api swagger,
		// TODO(teyckmans): not sure if can happen for CRDs
		groupVersionKind := map[string]string{}
		groupVersionKind["group"] = gvk.Group
		groupVersionKind["kind"] = gvk.Kind
		groupVersionKind["version"] = gvk.Version

		var groupVersionKinds []map[string]string
		groupVersionKinds = append(groupVersionKinds, groupVersionKind)

		extensions := map[string]interface{}{}
		extensions["x-kubernetes-group-version-kind"] = groupVersionKinds

		swaggerSchema.VendorExtensible = spec.VendorExtensible{
			Extensions: extensions,
		}
	}

	return swaggerSchema
}

func addProperty(propertyName string, property *apiext.JSONSchemaProps, swaggerSchemaProps *spec.SchemaProps, ctx *DefinitionsContext) {
	swaggerSchemaProps.Properties[propertyName] = jsonSchemaSimpleToSwaggerSchema(property, ctx)
}

func allOf(current *apiext.JSONSchemaProps, swaggerSchemaProps *spec.SchemaProps, ctx *DefinitionsContext) {
	for index := range current.AllOf {
		parent := current.AllOf[index]
		allOf(&parent, swaggerSchemaProps, ctx)

		if parent.Ref != nil {
			typeIdent := loadRef(*parent.Ref, ctx)

			if typeIdent != nil {
				refSchema := ctx.parser.Schemata[*typeIdent]

				for propertyName := range refSchema.Properties {
					property := refSchema.Properties[propertyName]
					addProperty(propertyName, &property, swaggerSchemaProps, ctx)
				}
			}
		}

		for propertyName := range parent.Properties {
			property := parent.Properties[propertyName]
			addProperty(propertyName, &property, swaggerSchemaProps, ctx)
		}
	}
}

func anyOf(current *apiext.JSONSchemaProps, swaggerSchemaProps *spec.SchemaProps, ctx *DefinitionsContext) {
	for index := range current.AnyOf {
		parent := current.AnyOf[index]

		if parent.Ref != nil {
			typeIdent := loadRef(*parent.Ref, ctx)
			if typeIdent != nil {
				refSchema := ctx.parser.Schemata[*typeIdent]

				for propertyName := range refSchema.Properties {
					property := refSchema.Properties[propertyName]
					addProperty(propertyName, &property, swaggerSchemaProps, ctx)
				}
			}
		}

		anyOf(&parent, swaggerSchemaProps, ctx)
		for propertyName := range parent.Properties {
			property := parent.Properties[propertyName]
			addProperty(propertyName, &property, swaggerSchemaProps, ctx)
		}
	}
}

func swaggerRef(rawReference string, ctx *DefinitionsContext) spec.Ref {
	referenceKey := CleanUpReference(rawReference, ctx.packageMappings)
	swaggerRef := "#/definitions/" + referenceKey

	if !strings.HasPrefix(referenceKey, "io.k8s") {
		_, alreadyAdded := ctx.swaggerSpec.Definitions[referenceKey]
		if !alreadyAdded {
			ctx.referencesToAdd[rawReference] = true
		}
	}

	return spec.MustCreateRef(swaggerRef)
}

func CleanUpReference(rawReference string, packageMappings map[string]string) string {
	var packagePart string
	var typeNamePart string
	typeNameSeparatorIndex := strings.LastIndex(rawReference, "~0")
	if typeNameSeparatorIndex != -1 {
		runes := []rune(rawReference)

		packagePart = string(runes[0:typeNameSeparatorIndex])
		typeNamePart = string(runes[typeNameSeparatorIndex+2:])
	}

	packagePart = strings.Replace(packagePart, "#/definitions/", "", -1)

	var packageName string

	// map crd type packages directly and their parents
	if mappedPackage, ok := packageMappings[packagePart]; ok {
		packageName = mappedPackage
	} else {
		// try to match on crd type packages and their parents and check if the start matches
		mappedByPrefix := false

		for packageKey := range packageMappings {
			cleanedPackageKey := strings.Replace(packageKey, "~1", ".", -1)
			cleanedPackagePart := strings.Replace(packagePart, "~1", ".", -1)

			if strings.HasPrefix(cleanedPackagePart, cleanedPackageKey) {
				mappedByPrefix = true
				mappedPackageStart := packageMappings[packageKey]
				mappedPackageSuffix := string([]rune(cleanedPackagePart)[len(cleanedPackageKey):])
				packageName = mappedPackageStart + mappedPackageSuffix
			}
		}

		if !mappedByPrefix {
			packageName = CleanUpReferencePart(packagePart)
		}
	}

	return packageName + "." + typeNamePart
}

func CleanUpReferencePart(rawReferencePart string) string {
	cleanedReferencePart := rawReferencePart

	cleanedReferencePart = strings.Replace(cleanedReferencePart, "#/definitions/", "", 1)
	cleanedReferencePart = strings.Replace(cleanedReferencePart, "~1", ".", -1)
	cleanedReferencePart = strings.Replace(cleanedReferencePart, "~0", ".", -1)

	parts := strings.Split(cleanedReferencePart, ".")
	cleanedReferencePart = parts[1] + "." + parts[0]
	if len(parts) > 2 {
		for i := 2; i < len(parts); i++ {
			cleanedReferencePart = cleanedReferencePart + "." + parts[i]
		}
	}

	return cleanedReferencePart
}
