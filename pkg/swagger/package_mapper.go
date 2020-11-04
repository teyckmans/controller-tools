package swagger

import (
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-tools/pkg/crd"
)

type PackageMapper struct {
	includedGroups      []string
	crdTypes            map[crd.TypeIdent]schema.GroupVersionKind
	groupMappingDetails []GroupMappingDetail
}

type GroupMappingDetail struct {
	groupName        string
	sharedPackage    string ""
	groupPackageName string ""
}

func (pm *PackageMapper) init() {
	for i := 0; i < len(pm.includedGroups); i++ {
		includedGroupName := pm.includedGroups[i]
		pm.initGroup(includedGroupName)
	}

}

func (pm *PackageMapper) initGroup(groupName string) {
	groupMappingDetail := GroupMappingDetail{
		groupName: groupName,
	}

	for typeIdent, gvk := range pm.crdTypes {

		if gvk.Group == groupName {

			if groupMappingDetail.sharedPackage == "" {
				groupMappingDetail.sharedPackage = typeIdent.Package.PkgPath
			} else {

				sharedPackageParts := strings.Split(groupMappingDetail.sharedPackage, "/")
				currentPackageParts := strings.Split(typeIdent.Package.PkgPath, "/")

				newSharedPackage := ""

				leastParts := len(sharedPackageParts)
				if len(currentPackageParts) < leastParts {
					leastParts = len(currentPackageParts)
				}

				for i := 0; i < leastParts; i++ {
					sharedPackagePart := sharedPackageParts[i]
					currentPackagePart := currentPackageParts[i]

					if sharedPackagePart == currentPackagePart {
						if newSharedPackage == "" {
							newSharedPackage = sharedPackagePart
						} else {
							newSharedPackage = newSharedPackage + "/" + sharedPackagePart
						}
					}
				}

				groupMappingDetail.sharedPackage = newSharedPackage
			}
		}
	}

	groupMappingDetail.groupPackageName = pm.groupToPackageName(groupMappingDetail.groupName)

	println("[" + groupName + "]sharedPackage = " + groupMappingDetail.sharedPackage + " => " + groupMappingDetail.groupPackageName)

	pm.groupMappingDetails = append(pm.groupMappingDetails, groupMappingDetail)

	for typeIdent, gvk := range pm.crdTypes {
		if gvk.Group == groupName {
			println("\t" + typeIdent.Package.PkgPath + "/" + typeIdent.Name + " => " + pm.mapTypeIdent(typeIdent))
		}
	}
}

func (pm *PackageMapper) groupToPackageName(groupName string) string {
	groupSplits := strings.Split(groupName, ".")
	packageName := ""
	for i := len(groupSplits) - 1; i >= 0; i-- {
		if len(packageName) == 0 {
			packageName = groupSplits[i]
		} else {
			packageName = packageName + "." + groupSplits[i]
		}
	}
	return packageName
}

func (pm *PackageMapper) mapTypeIdent(typeIdent crd.TypeIdent) string {
	return pm.mapPackageAndTypeName(typeIdent.Package.PkgPath, typeIdent.Name)
}

func (pm *PackageMapper) mapPackageAndTypeName(pkgPath string, typeName string) string {
	return pm.mapAbsoluteTypeName(pkgPath + "/" + typeName)
}

func (pm *PackageMapper) mapAbsoluteTypeName(rawType string) string {
	mappedPackage := ""

	groupMappingDetail := pm.matchingGroup(rawType)

	sharedPackage := ""
	if groupMappingDetail != nil {
		mappedPackage = groupMappingDetail.groupName
		sharedPackage = groupMappingDetail.sharedPackage
	}

	sharedPackageParts := strings.Split(sharedPackage, "/")
	amountOfSharedPackageParts := len(sharedPackageParts)
	typePackageParts := strings.Split(rawType, "/")

	for i := 0; i < len(typePackageParts); i++ {
		typePackagePart := typePackageParts[i]

		if i < amountOfSharedPackageParts {
			sharedPackagePart := sharedPackageParts[i]

			if sharedPackagePart != typePackagePart {
				if mappedPackage == "" {
					mappedPackage = typePackagePart
				} else {
					mappedPackage = mappedPackage + "." + typePackagePart
				}
			}
		} else {
			if mappedPackage == "" {
				mappedPackage = typePackagePart
			} else {
				mappedPackage = mappedPackage + "." + typePackagePart
			}
		}
	}

	parts := strings.Split(mappedPackage, ".")
	cleanedReferencePart := parts[1] + "." + parts[0]
	if len(parts) > 2 {
		for i := 2; i < len(parts); i++ {
			cleanedReferencePart = cleanedReferencePart + "." + parts[i]
		}
	}

	return cleanedReferencePart
}

func (pm *PackageMapper) matchingGroup(rawType string) *GroupMappingDetail {
	var resultDetail *GroupMappingDetail

	for i := 0; i < len(pm.groupMappingDetails); i++ {
		currentGroupMappingDetail := pm.groupMappingDetails[i]

		if strings.HasPrefix(rawType, currentGroupMappingDetail.sharedPackage) {
			match := false
			if resultDetail == nil {
				match = true
			} else {
				match = len(currentGroupMappingDetail.groupName) > len(resultDetail.groupName)
			}

			if match {
				resultDetail = &currentGroupMappingDetail
			}
		}
	}
	return resultDetail
}
