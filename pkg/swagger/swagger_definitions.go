package swagger

import (
	"strings"

	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-tools/pkg/crd"
	"sigs.k8s.io/controller-tools/pkg/loader"

	"github.com/go-openapi/spec"
)

type DefinitionsContext struct {
	swaggerSpec     *spec.Swagger
	parser          *crd.Parser
	roots           []*loader.Package
	referencesToAdd map[string]bool
	packageMapper   *PackageMapper
	processedTypes  []crd.TypeIdent
}

/**
Embed simple types so we only remain with object types as elements in the swagger definitions map
as is the case for the vanilla k8s api swagger.
*/
func addDefinitions(ctx *DefinitionsContext) error {
	err := addTypeDefinitions(ctx)
	if err != nil {
		return err
	}

	return nil
}

func isSimpleType(jsonSchema *apiext.JSONSchemaProps) bool {
	var isSimpleType bool

	if jsonSchema.Type == "" && jsonSchema.Ref != nil {
		// ref property
		isSimpleType = false
	} else if jsonSchema.Ref == nil && jsonSchema.Type == "" && jsonSchema.Format == "" && len(jsonSchema.AnyOf) == 0 {
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

func addTypeDefinitions(ctx *DefinitionsContext) error {
	parser := ctx.parser

	ctx.referencesToAdd = make(map[string]bool)

	for typeIdent := range parser.CrdTypes {
		jsonSchema := parser.Schemata[typeIdent]
		if !isSimpleType(&jsonSchema) {
			err := addTypeToSwaggerSpec(typeIdent, &jsonSchema, ctx.swaggerSpec, ctx)
			if err != nil {
				return err
			}
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
			if typeIdent != nil && !strings.HasPrefix(referenceToAdd, "#/definitions/k8s.io") {
				jsonSchema := ctx.parser.Schemata[*typeIdent]
				err := addTypeToSwaggerSpec(*typeIdent, &jsonSchema, ctx.swaggerSpec, ctx)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func swaggerDefinitionKey(typeIdent crd.TypeIdent, ctx *DefinitionsContext) string {
	jsonPackageName := strings.Replace(typeIdent.Package.PkgPath, "/", "~1", -1)

	return CleanUpReference(jsonPackageName+"~0"+typeIdent.Name, ctx)
}

func addTypeToSwaggerSpec(typeIdent crd.TypeIdent, jsonSchema *apiext.JSONSchemaProps, swaggerSpec *spec.Swagger, ctx *DefinitionsContext) error {

	if containsTypeIdent(ctx.processedTypes, typeIdent) {
		return nil
	}

	definitionKey := swaggerDefinitionKey(typeIdent, ctx)

	if strings.HasPrefix(definitionKey, "io.k8s") {
		// no need to include in crd-swagger file, included in api spec.
		return nil
	}

	if jsonSchema.Type == "object" {
		swaggerSpec.Definitions[definitionKey] = jsonSchemaObjectToSwaggerSchema(typeIdent, jsonSchema, ctx)
	} else if jsonSchema.Type == "" && jsonSchema.Ref != nil {
		swaggerSpec.Definitions[definitionKey] = jsonSchemaRefToSwaggerSchema(jsonSchema, ctx)
	} else {
		// TODO(teyckmans) if we get here there is something wrong with the embedding of references to simple types.
		println("!!SKIPPING!! " + definitionKey + " as it is not an object but of type: " + jsonSchema.Type + " with format " + jsonSchema.Format + " and ref " + *jsonSchema.Ref)
	}

	ctx.processedTypes = append(ctx.processedTypes, typeIdent)

	return nil
}

func containsTypeIdent(typeIdents []crd.TypeIdent, searchTypeIdent crd.TypeIdent) bool {
	for _, currentTypeIdent := range typeIdents {
		if currentTypeIdent == searchTypeIdent {
			return true
		}
	}
	return false
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
	} else if jsonSchema.Type == "date-time" {
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
	} else if jsonSchema.Format != "" {
		swaggerSchema = jsonSchemaTypeWithFormatToSwaggerSchema(jsonSchema)
	} else if len(jsonSchema.AnyOf) > 0 {
		swaggerSchema = jsonSchemaTypeWithAnyOfToSwaggerSchema(jsonSchema)
	} else {
		println("NEW jsonSchema.Type detected - default mapping used for type " + jsonSchema.Type + " format: " + jsonSchema.Format)
	}

	return swaggerSchema
}

func jsonSchemaTypeWithAnyOfToSwaggerSchema(jsonSchema *apiext.JSONSchemaProps) spec.Schema {
	format := ""

	if containsAnyOfWithType(jsonSchema, "integer") && containsAnyOfWithType(jsonSchema, "string") {
		format = "int-or-string"
	} else {
		format = "unknown"
	}

	return spec.Schema{
		SchemaProps: spec.SchemaProps{
			Description: jsonSchema.Description,
			Type:        []string{"string"},
			Format:      format,
			Required:    jsonSchema.Required,
		},
	}
}

func containsAnyOfWithType(jsonSchema *apiext.JSONSchemaProps, typeName string) bool {
	for i := 0; i < len(jsonSchema.AnyOf); i++ {
		anyOfSchema := jsonSchema.AnyOf[i]
		if anyOfSchema.Type == typeName {
			return true
		}
	}
	return false
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
	addRef := false

	embedRef := false
	var refJSONSchema *apiext.JSONSchemaProps

	refJSONSchema = followRefChain(jsonSchema, ctx)

	embedRef = refJSONSchema != nil && isSimpleType(refJSONSchema)

	var swaggerSchema spec.Schema

	if embedRef {
		copy := refJSONSchema.DeepCopy()
		copy.Description = jsonSchema.Description

		swaggerSchema = jsonSchemaSimpleToSwaggerSchema(copy, ctx)
	} else {
		addRef = true
	}

	if addRef {
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
	referenceKey := CleanUpReference(rawReference, ctx)
	swaggerRef := "#/definitions/" + referenceKey

	if !strings.HasPrefix(referenceKey, "io.k8s") {
		_, alreadyAdded := ctx.swaggerSpec.Definitions[referenceKey]
		if !alreadyAdded {
			ctx.referencesToAdd[rawReference] = true
		}
	}

	return spec.MustCreateRef(swaggerRef)
}

func CleanUpReference(rawReference string, ctx *DefinitionsContext) string {
	var packagePart string
	var typeNamePart string
	typeNameSeparatorIndex := strings.LastIndex(rawReference, "~0")
	if typeNameSeparatorIndex != -1 {
		runes := []rune(rawReference)

		packagePart = string(runes[0:typeNameSeparatorIndex])
		typeNamePart = string(runes[typeNameSeparatorIndex+2:])
	}

	packagePart = strings.Replace(packagePart, "#/definitions/", "", -1)
	cleanedPackagePart := strings.Replace(packagePart, "~1", "/", -1)

	return ctx.packageMapper.mapPackageAndTypeName(cleanedPackagePart, typeNamePart)
}
