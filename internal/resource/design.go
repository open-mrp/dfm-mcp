package resource

import (
	"net/http"
	"time"

	"github.com/open-mrp/apikit/example"
	"github.com/open-mrp/apikit/object"
	dfmconstants "github.com/open-mrp/dfm/constants"

	"github.com/open-mrp/dfm-mcp/internal/constants"
)

// A design uploaded ahead of a check. Creating one returns an upload target the file is sent to directly, so the file never passes through the API; a check is then asked for with the design's ID and runs in the background. The file is discarded once that check has run unless the check retained it, and an upload that is never checked expires on its own.
type Design struct {
	// Design ID.
	ID string `json:"id" validate:"required"`
	// Always "dfm_design".
	Object object.Type `json:"object" validate:"required,enum=dfm_design"`
	// Format of the design file.
	Format dfmconstants.DesignFormat `json:"format" validate:"required"`
	// Filename the caller gave, or null.
	Filename *string `json:"filename"`
	// Size of the file in bytes, as declared when the upload was created.
	SizeBytes int64 `json:"size_bytes" validate:"required"`
	// SHA-256 of the file, hex encoded, as declared when the design was created. Storage refuses an upload whose bytes do not match it.
	SHA256 string `json:"sha256" validate:"required"`
	// Where the file is: still expected, present, or discarded after its check ran.
	Status constants.DesignStatus `json:"status" validate:"required"`
	// How to send the file. Returned once, when the design is created; null afterwards.
	Upload *DesignUpload `json:"upload"`
	// When the upload target stops working and an unchecked file is removed.
	ExpiresAt time.Time `json:"expires_at" validate:"required"`
	// When the design was created.
	CreatedAt time.Time `json:"created_at" validate:"required"`
	// When the design last changed, such as its status.
	UpdatedAt time.Time `json:"updated_at" validate:"required"`
}

// DesignUpload says where and how to send the file.
type DesignUpload struct {
	// Signed URL to send the file to, as the raw request body, with no other credentials. Valid until `expires_at`. Treat it as a secret.
	URL string `json:"url" validate:"required"`
	// The HTTP method to use.
	Method string `json:"method" validate:"required"`
	// Headers the request must carry, exactly as given: the URL is bound to them, and `x-amz-checksum-sha256` makes storage refuse bytes that are not the declared file.
	Headers map[string]string `json:"headers" validate:"required"`
	// When the URL stops working.
	ExpiresAt time.Time `json:"expires_at" validate:"required"`
}

const SampleDesignID = "dfde_7k2m9qwe4rtz"

var sampleDesignFilename = "bracket.dxf"

var SampleDesign = &Design{
	ID:        SampleDesignID,
	Object:    constants.ObjectTypeDesign,
	Format:    dfmconstants.DesignFormatDXF,
	Filename:  &sampleDesignFilename,
	SizeBytes: 48213,
	SHA256:    SampleDesignSHA256,
	Status:    constants.DesignStatusAwaitingUpload,
	Upload: &DesignUpload{
		URL:       "https://augno-dfm-us-east-2.s3.us-east-2.amazonaws.com/uploads/apke_of6pr7ox32l0/dfde_7k2m9qwe4rtz.dxf?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Expires=86400&X-Amz-SignedHeaders=content-type%3Bhost%3Bx-amz-checksum-sha256%3Bx-amz-sdk-checksum-algorithm&X-Amz-Signature=...",
		Method:    http.MethodPut,
		Headers:   map[string]string{"Content-Type": "application/dxf", "x-amz-checksum-sha256": "n4bQgYhMfWWaL+qgxVrQFaO/TxsrC4Is0V1sFbDwCgg=", "x-amz-sdk-checksum-algorithm": "SHA256"},
		ExpiresAt: SampleCreatedAt.Add(24 * time.Hour),
	},
	ExpiresAt: SampleCreatedAt.Add(24 * time.Hour),
	CreatedAt: SampleCreatedAt,
	UpdatedAt: SampleCreatedAt,
}

// SampleDesignSHA256 is the hash of the sample design file, which the sample check ran on.
const SampleDesignSHA256 = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

func (*Design) SchemaExample() any {
	return example.ValidateAndMarshalToMap(SampleDesign)
}
