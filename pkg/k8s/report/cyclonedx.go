package report

import (
	"context"
	"io"

	cdx "github.com/CycloneDX/cyclonedx-go"

	"github.com/aquasecurity/trivy/pkg/sbom/cyclonedx/core"
)

// CycloneDXWriter implements types.Writer
type CycloneDXWriter struct {
	encoder   cdx.BOMEncoder
	marshaler *core.CycloneDX
}

// NewCycloneDXWriter constract new CycloneDXWriter
func NewCycloneDXWriter(output io.Writer, format cdx.BOMFileFormat, appVersion string) CycloneDXWriter {
	encoder := cdx.NewBOMEncoder(output, format)
	encoder.SetPretty(true)
	encoder.SetEscapeHTML(false)
	return CycloneDXWriter{
		encoder:   encoder,
		marshaler: core.NewCycloneDX(appVersion),
	}
}

func (w CycloneDXWriter) Write(ctx context.Context, component *core.Component) error {
	bom := w.marshaler.Marshal(ctx, component)
	return w.encoder.Encode(bom)
}
