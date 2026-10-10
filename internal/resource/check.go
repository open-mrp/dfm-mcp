package resource

import (
	"encoding/json"
	"time"

	"github.com/open-mrp/apikit/example"
	dfmconstants "github.com/open-mrp/dfm/constants"
	dfmresource "github.com/open-mrp/dfm/resource"

	"github.com/open-mrp/dfm-mcp/internal/constants"
)

// A DFM check is one run of the engine over a design: the engine's result, kept under an ID so it can be retrieved, reported on and replayed. The Go type is DFMCheck because the engine's result type, which it embeds, is already named Check; on the wire both are `dfm_check`.
type DFMCheck struct {
	// Check ID.
	ID string `json:"id" validate:"required"`
	dfmresource.Check
	// The design file the check ran on.
	Design *CheckDesign `json:"design" validate:"required"`
	// The stock the design was checked against, as sent.
	Stock json.RawMessage `json:"stock" validate:"required"`
	// ID of the capability preset the design was checked against, or null when the caller sent a capability of their own.
	CapabilityPreset *string `json:"capability_preset"`
	// Where the check ran: on this server, or in the caller's own copy of the engine and recorded here afterwards, in which case the result is the caller's claim.
	Origin constants.CheckOrigin `json:"origin" validate:"required"`
	// A link to a drawing of the design as the engine read it, with the findings marked. Null unless requested with `include[]=preview`, and null then too when the check was not asked for one (`preview: svg` on the check action). A check job's result carries it when the action asked for it.
	Preview *CheckPreview `json:"preview" expandable:"true"`
	// When the check ran.
	CreatedAt time.Time `json:"created_at" validate:"required"`
}

// CheckPreview is a link to a rendering of the design with the findings marked. The rendering is made in the background with the check and kept in storage; the API hands out a short-lived signed URL to it, never the drawing itself, so a drawing of any size loads straight from storage.
type CheckPreview struct {
	// Format of the rendering.
	Format constants.PreviewFormat `json:"format" validate:"required"`
	// Signed URL that serves the rendering with a GET and no credentials, such as from an `<img>` tag. Treat it as a secret: anyone holding it can load the drawing until `expires_at`.
	URL string `json:"url" validate:"required"`
	// When the URL stops working. Retrieve the check again with `include[]=preview` for a fresh one.
	ExpiresAt time.Time `json:"expires_at" validate:"required"`
}

// CheckDesign describes the design file of a check without carrying it.
type CheckDesign struct {
	// Format of the design file.
	Format dfmconstants.DesignFormat `json:"format" validate:"required"`
	// Filename the caller gave, or null.
	Filename *string `json:"filename"`
	// Size of the design file in bytes.
	SizeBytes int64 `json:"size_bytes" validate:"required"`
	// SHA-256 of the design file, hex encoded, so a caller can match a stored check to a file without the file being kept.
	SHA256 string `json:"sha256" validate:"required"`
	// Whether the service kept the design file.
	Retention constants.DesignRetention `json:"retention" validate:"required"`
}

const SampleCheckID = "dfck_3g20lmq8eflb"

var sampleCheckFilename = "bracket.dxf"

var SampleCheck = &DFMCheck{
	ID: SampleCheckID,
	Check: dfmresource.Check{
		Object:        dfmconstants.ObjectTypeDFMCheck,
		Process:       dfmconstants.ProcessLaserCutting,
		EngineVersion: "0.48.3",
		Verdict:       dfmconstants.VerdictNotManufacturable,
		ErrorCount:    1,
		PartCount:     1,
		Parts: dfmresource.NewList([]dfmresource.Part{{
			Object:     dfmconstants.ObjectTypeDFMPart,
			Number:     1,
			CopyCount:  1,
			Instances:  dfmresource.NewList([]dfmresource.PartInstance{}, dfmresource.PageInfo{}),
			EntityIDs:  []string{"1A", "1B", "1C", "1D"},
			Verdict:    dfmconstants.VerdictNotManufacturable,
			ErrorCount: 1,
			Issues: dfmresource.NewList([]dfmresource.Issue{{
				Object:      dfmconstants.ObjectTypeDFMIssue,
				Code:        "hole_too_small",
				Severity:    "error",
				Message:     "A hole is smaller than the laser can cut cleanly in this stock.",
				Remediation: "Enlarge the hole to at least the required diameter, or drill it after cutting.",
				Required:    &dfmresource.Quantity{Value: "1.5", Unit: "mm"},
				Occurrences: dfmresource.NewList([]dfmresource.Occurrence{{
					Object:    dfmconstants.ObjectTypeDFMOccurrence,
					ID:        "occ_hole_too_small_1a2b3c",
					EntityIDs: []string{"2F"},
					Actual:    &dfmresource.Quantity{Value: "1.2", Unit: "mm"},
					Location:  &dfmresource.Point{X: "35.5", Y: "20", Unit: "mm"},
				}}, dfmresource.PageInfo{}),
			}}, dfmresource.PageInfo{}),
		}}, dfmresource.PageInfo{}),
		DrawingIssues: dfmresource.NewList([]dfmresource.Issue{}, dfmresource.PageInfo{}),
		SkippedChecks: dfmresource.NewList([]dfmresource.SkippedCheck{}, dfmresource.PageInfo{}),
	},
	Design: &CheckDesign{
		Format:    dfmconstants.DesignFormatDXF,
		Filename:  &sampleCheckFilename,
		SizeBytes: 48213,
		SHA256:    SampleDesignSHA256,
		Retention: constants.DesignRetentionNone,
	},
	Stock:            json.RawMessage(`{"material_code":"mild_steel","thickness":{"value":"1.5","unit":"mm"}}`),
	CapabilityPreset: &SampleCapabilityPresetCode,
	Origin:           constants.CheckOriginServer,
	Preview: &CheckPreview{
		Format:    constants.PreviewFormatSVG,
		URL:       "https://augno-dfm-us-east-2.s3.us-east-2.amazonaws.com/previews/dfck_3g20lmq8eflb.svg?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Expires=900&response-content-type=image%2Fsvg%2Bxml&X-Amz-Signature=...",
		ExpiresAt: SampleCreatedAt.Add(15 * time.Minute),
	},
	CreatedAt: SampleCreatedAt,
}

func (*DFMCheck) SchemaExample() any {
	return example.ValidateAndMarshalToMap(SampleCheck)
}
