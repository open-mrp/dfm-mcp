package resource

import (
	"time"

	"github.com/open-mrp/apikit/example"
	"github.com/open-mrp/apikit/object"
	dfmconstants "github.com/open-mrp/dfm/constants"
	dfmresource "github.com/open-mrp/dfm/resource"

	"github.com/open-mrp/dfm-mcp/internal/constants"
)

// A feedback report says a check's result was wrong: a finding that should not be there, one that is missing, or a measurement that is off, with the evidence behind the claim. Reports are what the engine is improved from.
type FeedbackReport struct {
	// Feedback report ID.
	ID string `json:"id" validate:"required"`
	// Always "dfm_feedback_report".
	Object object.Type `json:"object" validate:"required,enum=dfm_feedback_report"`
	// The check the report is about. Null unless requested with `include[]=check`.
	Check *DFMCheck `json:"check" expandable:"true"`
	// What kind of mistake is reported, which decides whether `occurrence_id` or `expected` applies.
	Type constants.FeedbackReportType `json:"type" validate:"required"`
	// ID of the occurrence that is wrong, for a false positive or a wrong measurement; null for a false negative.
	OccurrenceID *string `json:"occurrence_id"`
	// The finding that should be there (false negative) or its correct value (wrong measurement); null for a false positive.
	Expected *ExpectedFinding `json:"expected"`
	// Why the result is wrong, in the reporter's words.
	Reason string `json:"reason" validate:"required"`
	// What the report rests on.
	Evidence *Evidence `json:"evidence" validate:"required"`
	// Whether the design file is kept with the report so the engine can be improved on it.
	Design *FeedbackDesign `json:"design" validate:"required"`
	// When the report was made.
	CreatedAt time.Time `json:"created_at" validate:"required"`
}

// ExpectedFinding describes the finding a result should contain. Only the members given are compared.
type ExpectedFinding struct {
	// Finding code.
	Code dfmconstants.FindingCode `json:"code" validate:"required"`
	// Severity the finding should have, or null when not stated.
	Severity *dfmconstants.Severity `json:"severity"`
	// Entities the finding involves.
	EntityIDs []string `json:"entity_ids" validate:"required"`
	// A point within 0.01 mm of the finding, or null.
	Location *dfmresource.Point `json:"location"`
	// The correct measured value, or null.
	Actual *dfmresource.Quantity `json:"actual"`
}

// Evidence is what a report rests on.
type Evidence struct {
	// How the reporter knows.
	Type constants.EvidenceType `json:"type" validate:"required"`
	// Details: who, when, an order or message reference. Null when none were given.
	Detail *string `json:"detail"`
}

// FeedbackDesign says whether the design is kept with the report.
type FeedbackDesign struct {
	// Whether the design file is kept.
	Retention constants.DesignRetention `json:"retention" validate:"required"`
}

const SampleFeedbackReportID = "dffb_d60uqf2u7f7w"

var (
	sampleOccurrenceID   = "occ_hole_too_small_1a2b3c"
	sampleEvidenceDetail = "Laser shop cut the 1.2 mm holes cleanly on the 2026-10-08 order."
)

var SampleFeedbackReport = &FeedbackReport{
	ID:           SampleFeedbackReportID,
	Object:       constants.ObjectTypeFeedbackReport,
	Type:         constants.FeedbackReportTypeFalsePositive,
	OccurrenceID: &sampleOccurrenceID,
	Reason:       "The shop cuts 1.2 mm holes in 1.5 mm mild steel without trouble.",
	Evidence:     &Evidence{Type: constants.EvidenceTypePartMade, Detail: &sampleEvidenceDetail},
	Design:       &FeedbackDesign{Retention: constants.DesignRetentionRetained},
	CreatedAt:    SampleCreatedAt,
}

func (*FeedbackReport) SchemaExample() any {
	return example.ValidateAndMarshalToMap(SampleFeedbackReport)
}
