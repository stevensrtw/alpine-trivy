package cyclonedx

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	cdx "github.com/CycloneDX/cyclonedx-go"
	"github.com/package-url/packageurl-go"
	"github.com/samber/lo"
	"golang.org/x/xerrors"

	"github.com/aquasecurity/trivy/pkg/digest"
	ftypes "github.com/aquasecurity/trivy/pkg/fanal/types"
	"github.com/aquasecurity/trivy/pkg/purl"
	"github.com/aquasecurity/trivy/pkg/sbom/cyclonedx/core"
	"github.com/aquasecurity/trivy/pkg/scanner/utils"
	"github.com/aquasecurity/trivy/pkg/types"
)

const (
	PropertySchemaVersion = "SchemaVersion"
	PropertyType          = "Type"
	PropertyClass         = "Class"

	// Image properties
	PropertySize       = "Size"
	PropertyImageID    = "ImageID"
	PropertyRepoDigest = "RepoDigest"
	PropertyDiffID     = "DiffID"
	PropertyRepoTag    = "RepoTag"

	// Package properties
	PropertyPkgID           = "PkgID"
	PropertyPkgType         = "PkgType"
	PropertySrcName         = "SrcName"
	PropertySrcVersion      = "SrcVersion"
	PropertySrcRelease      = "SrcRelease"
	PropertySrcEpoch        = "SrcEpoch"
	PropertyModularitylabel = "Modularitylabel"
	PropertyFilePath        = "FilePath"
	PropertyLayerDigest     = "LayerDigest"
	PropertyLayerDiffID     = "LayerDiffID"
)

var (
	ErrInvalidBOMLink = xerrors.New("invalid bomLink format error")
)

type Marshaler struct {
	core *core.CycloneDX
}

func NewMarshaler(version string) *Marshaler {
	return &Marshaler{
		core: core.NewCycloneDX(version),
	}
}

// Marshal converts the Trivy report to the CycloneDX format
func (e *Marshaler) Marshal(ctx context.Context, report types.Report) (*cdx.BOM, error) {
	// Convert
	root, err := e.MarshalReport(report)
	if err != nil {
		return nil, xerrors.Errorf("failed to marshal report: %w", err)
	}

	return e.core.Marshal(ctx, root), nil
}

func (e *Marshaler) MarshalReport(r types.Report) (*core.Component, error) {
	// Metadata component
	root, err := e.rootComponent(r)
	if err != nil {
		return nil, err
	}

	for _, result := range r.Results {
		components, err := e.marshalResult(r.Metadata, result)
		if err != nil {
			return nil, err
		}
		root.Components = append(root.Components, components...)
	}
	return root, nil
}

func (e *Marshaler) marshalResult(metadata types.Metadata, result types.Result) ([]*core.Component, error) {
	if result.Type == ftypes.NodePkg || result.Type == ftypes.PythonPkg ||
		result.Type == ftypes.GemSpec || result.Type == ftypes.Jar || result.Type == ftypes.CondaPkg {
		// If a package is language-specific package that isn't associated with a lock file,
		// it will be a dependency of a component under "metadata".
		// e.g.
		//   Container component (alpine:3.15) ----------------------- #1
		//     -> Library component (npm package, express-4.17.3) ---- #2
		//     -> Library component (python package, django-4.0.2) --- #2
		//     -> etc.
		// ref. https://cyclonedx.org/use-cases/#inventory

		// Dependency graph from #1 to #2
		components, err := e.marshalPackages(metadata, result)
		if err != nil {
			return nil, err
		}
		return components, nil
	} else if result.Class == types.ClassOSPkg || result.Class == types.ClassLangPkg {
		// If a package is OS package, it will be a dependency of "Operating System" component.
		// e.g.
		//   Container component (alpine:3.15) --------------------- #1
		//     -> Operating System Component (Alpine Linux 3.15) --- #2
		//       -> Library component (bash-4.12) ------------------ #3
		//       -> Library component (vim-8.2)   ------------------ #3
		//       -> etc.
		//
		// Else if a package is language-specific package associated with a lock file,
		// it will be a dependency of "Application" component.
		// e.g.
		//   Container component (alpine:3.15) ------------------------ #1
		//     -> Application component (/app/package-lock.json) ------ #2
		//       -> Library component (npm package, express-4.17.3) --- #3
		//       -> Library component (npm package, lodash-4.17.21) --- #3
		//       -> etc.

		// #2
		appComponent := e.resultComponent(result, metadata.OS)

		// #3
		components, err := e.marshalPackages(metadata, result)
		if err != nil {
			return nil, err
		}

		// Dependency graph from #2 to #3
		appComponent.Components = components

		// Dependency graph from #1 to #2
		return []*core.Component{appComponent}, nil
	}
	return nil, nil
}

func (e *Marshaler) marshalPackages(metadata types.Metadata, result types.Result) ([]*core.Component, error) {
	// Get dependency parents first
	parents := ftypes.Packages(result.Packages).ParentDeps()

	// Group vulnerabilities by package ID
	vulns := lo.GroupBy(result.Vulnerabilities, func(v types.DetectedVulnerability) string {
		return lo.Ternary(v.PkgID == "", fmt.Sprintf("%s@%s", v.PkgName, v.InstalledVersion), v.PkgID)
	})

	// Create package map
	pkgs := lo.SliceToMap(result.Packages, func(pkg ftypes.Package) (string, Package) {
		pkgID := lo.Ternary(pkg.ID == "", fmt.Sprintf("%s@%s", pkg.Name, utils.FormatVersion(pkg)), pkg.ID)
		return pkgID, Package{
			Type:            result.Type,
			Metadata:        metadata,
			Package:         pkg,
			Vulnerabilities: vulns[pkgID],
		}
	})

	var directComponents []*core.Component
	for _, pkg := range pkgs {
		// Skip indirect dependencies
		if pkg.Indirect && len(parents[pkg.ID]) != 0 {
			continue
		}

		// Recursive packages from direct dependencies
		if component, err := e.marshalPackage(pkg, pkgs, make(map[string]*core.Component)); err != nil {
			return nil, nil
		} else if component != nil {
			directComponents = append(directComponents, component)
		}
	}

	return directComponents, nil
}

type Package struct {
	ftypes.Package
	Type            ftypes.TargetType
	Metadata        types.Metadata
	Vulnerabilities []types.DetectedVulnerability
}

func (e *Marshaler) marshalPackage(pkg Package, pkgs map[string]Package, components map[string]*core.Component,
) (*core.Component, error) {
	if c, ok := components[pkg.ID]; ok {
		return c, nil
	}

	component, err := pkgComponent(pkg)
	if err != nil {
		return nil, xerrors.Errorf("failed to parse pkg: %w", err)
	}

	// Skip component that can't be converted from `Package`
	if component == nil {
		return nil, nil
	}
	components[pkg.ID] = component

	// Iterate dependencies
	for _, dep := range pkg.DependsOn {
		childPkg, ok := pkgs[dep]
		if !ok {
			continue
		}

		child, err := e.marshalPackage(childPkg, pkgs, components)
		if err != nil {
			return nil, xerrors.Errorf("failed to parse pkg: %w", err)
		}
		component.Components = append(component.Components, child)
	}
	return component, nil
}

func (e *Marshaler) rootComponent(r types.Report) (*core.Component, error) {
	root := &core.Component{
		Name: r.ArtifactName,
	}

	props := []core.Property{
		{
			Name:  PropertySchemaVersion,
			Value: strconv.Itoa(r.SchemaVersion),
		},
	}

	switch r.ArtifactType {
	case ftypes.ArtifactContainerImage:
		root.Type = cdx.ComponentTypeContainer
		props = append(props, core.Property{
			Name:  PropertyImageID,
			Value: r.Metadata.ImageID,
		})

		p, err := purl.New(purl.TypeOCI, r.Metadata, ftypes.Package{})
		if err != nil {
			return nil, xerrors.Errorf("failed to new package url for oci: %w", err)
		}
		if p != nil {
			root.PackageURL = p
		}

	case ftypes.ArtifactVM:
		root.Type = cdx.ComponentTypeContainer
	case ftypes.ArtifactFilesystem, ftypes.ArtifactRepository:
		root.Type = cdx.ComponentTypeApplication
	case ftypes.ArtifactCycloneDX:
		return toCoreComponent(r.CycloneDX.Metadata.Component)
	}

	if r.Metadata.Size != 0 {
		props = append(props, core.Property{
			Name:  PropertySize,
			Value: strconv.FormatInt(r.Metadata.Size, 10),
		})
	}

	if len(r.Metadata.RepoDigests) > 0 {
		props = append(props, core.Property{
			Name:  PropertyRepoDigest,
			Value: strings.Join(r.Metadata.RepoDigests, ","),
		})
	}
	if len(r.Metadata.DiffIDs) > 0 {
		props = append(props, core.Property{
			Name:  PropertyDiffID,
			Value: strings.Join(r.Metadata.DiffIDs, ","),
		})
	}
	if len(r.Metadata.RepoTags) > 0 {
		props = append(props, core.Property{
			Name:  PropertyRepoTag,
			Value: strings.Join(r.Metadata.RepoTags, ","),
		})
	}

	root.Properties = filterProperties(props)

	return root, nil
}

func (e *Marshaler) resultComponent(r types.Result, osFound *ftypes.OS) *core.Component {
	component := &core.Component{
		Name: r.Target,
		Properties: []core.Property{
			{
				Name:  PropertyType,
				Value: string(r.Type),
			},
			{
				Name:  PropertyClass,
				Value: string(r.Class),
			},
		},
	}

	switch r.Class {
	case types.ClassOSPkg:
		// UUID needs to be generated since Operating System Component cannot generate PURL.
		// https://cyclonedx.org/use-cases/#known-vulnerabilities
		if osFound != nil {
			component.Name = string(osFound.Family)
			component.Version = osFound.Name
		}
		component.Type = cdx.ComponentTypeOS
	case types.ClassLangPkg:
		// UUID needs to be generated since Application Component cannot generate PURL.
		// https://cyclonedx.org/use-cases/#known-vulnerabilities
		component.Type = cdx.ComponentTypeApplication
	}

	return component
}

func pkgComponent(pkg Package) (*core.Component, error) {
	name := pkg.Name
	version := pkg.Version
	var group string
	// there are cases when we can't build purl
	// e.g. local Go packages
	if pu := pkg.Identifier.PURL; pu != nil {
		version = pu.Version
		// Use `group` field for GroupID and `name` for ArtifactID for java files
		// https://github.com/aquasecurity/trivy/issues/4675
		// Use `group` field for npm scopes
		// https://github.com/aquasecurity/trivy/issues/5908
		if pu.Type == packageurl.TypeMaven || pu.Type == packageurl.TypeNPM {
			name = pu.Name
			group = pu.Namespace
		}
	}

	properties := []core.Property{
		{
			Name:  PropertyPkgID,
			Value: pkg.ID,
		},
		{
			Name:  PropertyPkgType,
			Value: string(pkg.Type),
		},
		{
			Name:  PropertyFilePath,
			Value: pkg.FilePath,
		},
		{
			Name:  PropertySrcName,
			Value: pkg.SrcName,
		},
		{
			Name:  PropertySrcVersion,
			Value: pkg.SrcVersion,
		},
		{
			Name:  PropertySrcRelease,
			Value: pkg.SrcRelease,
		},
		{
			Name:  PropertySrcEpoch,
			Value: strconv.Itoa(pkg.SrcEpoch),
		},
		{
			Name:  PropertyModularitylabel,
			Value: pkg.Modularitylabel,
		},
		{
			Name:  PropertyLayerDigest,
			Value: pkg.Layer.Digest,
		},
		{
			Name:  PropertyLayerDiffID,
			Value: pkg.Layer.DiffID,
		},
	}

	return &core.Component{
		Type:            cdx.ComponentTypeLibrary,
		Name:            name,
		Group:           group,
		Version:         version,
		PackageURL:      purl.WithPath(pkg.Identifier.PURL, pkg.FilePath),
		Supplier:        pkg.Maintainer,
		Licenses:        pkg.Licenses,
		Hashes:          lo.Ternary(pkg.Digest == "", nil, []digest.Digest{pkg.Digest}),
		Properties:      filterProperties(properties),
		Vulnerabilities: pkg.Vulnerabilities,
	}, nil
}

func filterProperties(props []core.Property) []core.Property {
	return lo.Filter(props, func(property core.Property, index int) bool {
		return !(property.Value == "" || (property.Name == PropertySrcEpoch && property.Value == "0"))
	})
}

func toCoreComponent(c ftypes.Component) (*core.Component, error) {
	var props []core.Property
	for _, prop := range c.Properties {
		var namespace string
		name := prop.Name
		// Separate the Trivy namespace to avoid double spelling of namespaces.
		if strings.HasPrefix(name, core.Namespace) {
			name = strings.TrimPrefix(name, core.Namespace)
			namespace = core.Namespace
		}
		props = append(props, core.Property{
			Namespace: namespace,
			Name:      name,
			Value:     prop.Value,
		})
	}
	var p *purl.PackageURL
	if c.PackageURL != "" {
		var err error
		p, err = purl.FromString(c.PackageURL)
		if err != nil {
			return nil, xerrors.Errorf("failed to parse purl: %w", err)
		}
	}

	return &core.Component{
		Name:       c.Name,
		Group:      c.Group,
		PackageURL: p,
		Type:       cdx.ComponentType(c.Type),
		Properties: props,
	}, nil
}
