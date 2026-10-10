// Package constants holds the enums and identifiers of this API that the engine does not define: object types of its own resources, ID prefixes and the design retention choice.
package constants

import (
	"github.com/open-mrp/apikit/id"
	"github.com/open-mrp/apikit/object"
	dfmconstants "github.com/open-mrp/dfm/constants"
)

// Object types of this API's own resources. A stored check keeps the engine's dfm_check object type, since it is the engine's result with an identity.
const (
	ObjectTypeAPIKey           object.Type = "dfm_api_key"         // #nosec G101 - an object type, not a credential
	ObjectTypeCreatedAPIKey    object.Type = "dfm_created_api_key" // #nosec G101
	ObjectTypeFeedbackReport   object.Type = "dfm_feedback_report"
	ObjectTypeCapabilityPreset object.Type = "dfm_capability_preset"
	ObjectTypeTelemetryReport  object.Type = "dfm_telemetry_report"
	ObjectTypeDesign           object.Type = "dfm_design"
	ObjectTypeCheck            object.Type = object.Type(dfmconstants.ObjectTypeDFMCheck)
)

// ID prefixes of the public IDs internal/ids formats, composed from two-letter vocabulary words: ap=API, ke=key, df=DFM, ck=check, fb=feedback, tm=telemetry, de=design, jb=job, ik=idempotency key.
var (
	APIKeyIDPrefix          = id.ComposePrefix("ap", "ke")
	CheckIDPrefix           = id.ComposePrefix("df", "ck")
	FeedbackReportIDPrefix  = id.ComposePrefix("df", "fb")
	TelemetryReportIDPrefix = id.ComposePrefix("df", "tm")
	DesignIDPrefix          = id.ComposePrefix("df", "de")
	JobIDPrefix             = id.ComposePrefix("df", "jb")
	IdempotencyKeyIDPrefix  = id.ComposePrefix("df", "ik")
)

// Kinds of background work, the async_job's `type`.
const (
	// JobTypeCheck is a check of an uploaded design.
	JobTypeCheck = "check"
	// JobTypeRenderPreview is a drawing of a stored check whose design was kept.
	JobTypeRenderPreview = "render_preview"
)

// DesignStatus says where an uploaded design file is. A design is created awaiting its file, becomes uploaded when a check is asked for and the file is there, and is discarded once the check has run, since the file is kept only on the check that retained it.
type DesignStatus string

const (
	DesignStatusAwaitingUpload DesignStatus = "awaiting_upload"
	DesignStatusUploaded       DesignStatus = "uploaded"
	DesignStatusDiscarded      DesignStatus = "discarded"
)

func (s DesignStatus) IsValid() bool {
	switch s {
	case DesignStatusAwaitingUpload, DesignStatusUploaded, DesignStatusDiscarded:
		return true
	default:
		return false
	}
}

func (s DesignStatus) EnumValues() []string {
	return []string{string(DesignStatusAwaitingUpload), string(DesignStatusUploaded), string(DesignStatusDiscarded)}
}

// DesignRetention says whether the service keeps the design file after a check. Designs are often proprietary, so the default keeps nothing: the caller opts in so the engine can be improved on real drawings.
type DesignRetention string

const (
	// DesignRetentionNone keeps only the result; the design file is discarded once the check has run.
	DesignRetentionNone DesignRetention = "none"
	// DesignRetentionRetained keeps the design file, encrypted at rest, so the check can be replayed on newer engines and a feedback report can be reproduced.
	DesignRetentionRetained DesignRetention = "retained"
)

func (r DesignRetention) IsValid() bool {
	switch r {
	case DesignRetentionNone, DesignRetentionRetained:
		return true
	default:
		return false
	}
}

func (r DesignRetention) EnumValues() []string {
	return []string{string(DesignRetentionNone), string(DesignRetentionRetained)}
}

// CheckOrigin says where a check ran: on the server, on the design the caller sent, or in the caller's own copy of the engine and recorded afterwards. A client check's result and design hash are the client's claim, not the server's.
type CheckOrigin string

const (
	CheckOriginServer CheckOrigin = "server"
	CheckOriginClient CheckOrigin = "client"
)

func (o CheckOrigin) IsValid() bool {
	switch o {
	case CheckOriginServer, CheckOriginClient:
		return true
	default:
		return false
	}
}

func (o CheckOrigin) EnumValues() []string {
	return []string{string(CheckOriginServer), string(CheckOriginClient)}
}

// PreviewFormat is the drawing a check can return alongside its result: none, or an SVG of the design as the engine read it with the findings marked.
type PreviewFormat string

const (
	PreviewFormatNone PreviewFormat = "none"
	PreviewFormatSVG  PreviewFormat = "svg"
)

func (f PreviewFormat) IsValid() bool {
	switch f {
	case PreviewFormatNone, PreviewFormatSVG:
		return true
	default:
		return false
	}
}

func (f PreviewFormat) EnumValues() []string {
	return []string{string(PreviewFormatNone), string(PreviewFormatSVG)}
}

// APIKeyStatus says whether a key still authenticates requests. It follows from `revoked_at` and is never set directly: a key is revoked with its revoke or rotate action.
type APIKeyStatus string

const (
	// APIKeyStatusActive authenticates requests.
	APIKeyStatusActive APIKeyStatus = "active"
	// APIKeyStatusRevoked no longer authenticates requests; its record is kept.
	APIKeyStatusRevoked APIKeyStatus = "revoked"
)

func (s APIKeyStatus) IsValid() bool {
	switch s {
	case APIKeyStatusActive, APIKeyStatusRevoked:
		return true
	default:
		return false
	}
}

func (s APIKeyStatus) EnumValues() []string {
	return []string{string(APIKeyStatusActive), string(APIKeyStatusRevoked)}
}

// FeedbackReportType says how a check's result was wrong, and so which of a report's fields apply: `occurrence_id` for a finding that is there, `expected` for one that should be.
type FeedbackReportType string

const (
	// FeedbackReportTypeFalsePositive is a finding that should not be there; the report names it by `occurrence_id`.
	FeedbackReportTypeFalsePositive FeedbackReportType = "false_positive"
	// FeedbackReportTypeFalseNegative is a finding that is missing; the report describes it in `expected`, with `entity_ids` or a `location`.
	FeedbackReportTypeFalseNegative FeedbackReportType = "false_negative"
	// FeedbackReportTypeWrongMeasurement is a finding whose measured value is wrong; the report names it by `occurrence_id` and gives the correct `expected.actual`.
	FeedbackReportTypeWrongMeasurement FeedbackReportType = "wrong_measurement"
)

func (t FeedbackReportType) IsValid() bool {
	switch t {
	case FeedbackReportTypeFalsePositive, FeedbackReportTypeFalseNegative, FeedbackReportTypeWrongMeasurement:
		return true
	default:
		return false
	}
}

func (t FeedbackReportType) EnumValues() []string {
	return []string{string(FeedbackReportTypeFalsePositive), string(FeedbackReportTypeFalseNegative), string(FeedbackReportTypeWrongMeasurement)}
}

// EvidenceType says what a feedback report rests on.
type EvidenceType string

const (
	// EvidenceTypeFabricatorAccepted is a fabricator accepting the part as drawn.
	EvidenceTypeFabricatorAccepted EvidenceType = "fabricator_accepted"
	// EvidenceTypeFabricatorRejected is a fabricator refusing or questioning the part.
	EvidenceTypeFabricatorRejected EvidenceType = "fabricator_rejected"
	// EvidenceTypePartMade is a part made as drawn.
	EvidenceTypePartMade EvidenceType = "part_made"
	// EvidenceTypePartFailed is a part that could not be made, or failed.
	EvidenceTypePartFailed EvidenceType = "part_failed"
	// EvidenceTypeExpertJudgment is the reporter's own expertise.
	EvidenceTypeExpertJudgment EvidenceType = "expert_judgment"
	// EvidenceTypeUserReport is a report with no further evidence.
	EvidenceTypeUserReport EvidenceType = "user_report"
)

func (t EvidenceType) IsValid() bool {
	switch t {
	case EvidenceTypeFabricatorAccepted, EvidenceTypeFabricatorRejected, EvidenceTypePartMade, EvidenceTypePartFailed, EvidenceTypeExpertJudgment, EvidenceTypeUserReport:
		return true
	default:
		return false
	}
}

func (t EvidenceType) EnumValues() []string {
	return []string{string(EvidenceTypeFabricatorAccepted), string(EvidenceTypeFabricatorRejected), string(EvidenceTypePartMade), string(EvidenceTypePartFailed), string(EvidenceTypeExpertJudgment), string(EvidenceTypeUserReport)}
}
