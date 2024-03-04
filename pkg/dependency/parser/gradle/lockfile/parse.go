package lockfile

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/aquasecurity/trivy/pkg/dependency/parser/types"
	"github.com/aquasecurity/trivy/pkg/dependency/parser/utils"
	xio "github.com/aquasecurity/trivy/pkg/x/io"
)

type Parser struct{}

func NewParser() types.Parser {
	return &Parser{}
}

func (Parser) Parse(r xio.ReadSeekerAt) ([]types.Library, []types.Dependency, error) {
	var libs []types.Library
	scanner := bufio.NewScanner(r)
	var lineNum int
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") { // skip comments
			continue
		}

		// dependency format: group:artifact:version=classPaths
		dep := strings.Split(line, ":")
		if len(dep) != 3 { // skip the last line with lists of empty configurations
			continue
		}

		name := strings.Join(dep[:2], ":")
		version := strings.Split(dep[2], "=")[0] // remove classPaths
		libs = append(libs, types.Library{
			ID:      fmt.Sprintf("%s:%s", name, version),
			Name:    name,
			Version: version,
			Locations: []types.Location{
				{
					StartLine: lineNum,
					EndLine:   lineNum,
				},
			},
		})

	}
	return utils.UniqueLibraries(libs), nil, nil
}
