package swift

import (
	"io"
	"sort"
	"strings"

	"github.com/liamg/jfather"
	"github.com/samber/lo"
	"golang.org/x/xerrors"

	"github.com/aquasecurity/trivy/pkg/dependency/parser/types"
	"github.com/aquasecurity/trivy/pkg/dependency/parser/utils"
	"github.com/aquasecurity/trivy/pkg/log"
	xio "github.com/aquasecurity/trivy/pkg/x/io"
)

// Parser is a parser for Package.resolved files
type Parser struct{}

func NewParser() types.Parser {
	return &Parser{}
}

func (Parser) Parse(r xio.ReadSeekerAt) ([]types.Library, []types.Dependency, error) {
	var lockFile LockFile
	input, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, xerrors.Errorf("read error: %w", err)
	}
	if err := jfather.Unmarshal(input, &lockFile); err != nil {
		return nil, nil, xerrors.Errorf("decode error: %w", err)
	}

	var libs types.Libraries
	pins := lockFile.Object.Pins
	if lockFile.Version > 1 {
		pins = lockFile.Pins
	}
	for _, pin := range pins {
		name := libraryName(pin, lockFile.Version)

		// Skip packages for which we cannot resolve the version
		if pin.State.Version == "" && pin.State.Branch == "" {
			log.Logger.Warnf("Unable to resolve %q. Both the version and branch fields are empty.", name)
			continue
		}

		// A Pin can be resolved using `branch` without `version`.
		// e.g. https://github.com/element-hq/element-ios/blob/6a9bcc88ea37147efba8f0a7bcf3ec187f4a4011/Riot.xcworkspace/xcshareddata/swiftpm/Package.resolved#L84-L92
		version := lo.Ternary(pin.State.Version != "", pin.State.Version, pin.State.Branch)

		libs = append(libs, types.Library{
			ID:      utils.PackageID(name, version),
			Name:    name,
			Version: version,
			Locations: []types.Location{
				{
					StartLine: pin.StartLine,
					EndLine:   pin.EndLine,
				},
			},
		})
	}
	sort.Sort(libs)
	return libs, nil, nil
}

func libraryName(pin Pin, lockVersion int) string {
	// Package.resolved v1 uses `RepositoryURL`
	// v2 uses `Location`
	name := pin.RepositoryURL
	if lockVersion > 1 {
		name = pin.Location
	}
	// Swift uses `https://github.com/<author>/<package>.git format
	// `.git` suffix can be omitted (take a look happy test)
	// Remove `https://` and `.git` to fit the same format
	name = strings.TrimPrefix(name, "https://")
	name = strings.TrimSuffix(name, ".git")
	return name
}

// UnmarshalJSONWithMetadata needed to detect start and end lines of deps for v1
func (p *Pin) UnmarshalJSONWithMetadata(node jfather.Node) error {
	if err := node.Decode(&p); err != nil {
		return err
	}
	// Decode func will overwrite line numbers if we save them first
	p.StartLine = node.Range().Start.Line
	p.EndLine = node.Range().End.Line
	return nil
}
