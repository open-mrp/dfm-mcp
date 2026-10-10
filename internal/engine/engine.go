// Package engine runs the DFM engine for the API: it assembles the engine's check-request document from what a caller sent, runs the check, and presents the result. The document is what the API stores, so a stored check replays with the engine's own tools.
package engine

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/open-mrp/apikit/apierror"
	"github.com/open-mrp/dfm"
	"github.com/open-mrp/dfm/capabilities"
	dfmconstants "github.com/open-mrp/dfm/constants"
	dfmresource "github.com/open-mrp/dfm/resource"

	"github.com/open-mrp/dfm-mcp/internal/apierrors"
)

// Input is what a check is built from. Stock, capability, hole operations and bends travel as the JSON the caller sent: the engine's strict decoder validates them and names the failing parameter.
type Input struct {
	Format           dfmconstants.DesignFormat
	Units            *dfmconstants.LengthUnit
	Adjustments      json.RawMessage
	Stock            json.RawMessage
	Capability       json.RawMessage
	CapabilityPreset *string
	HoleOperations   json.RawMessage
	Bends            json.RawMessage
}

// document is the engine's check-request document (schema check-request), in its field order.
type document struct {
	Design         designDocument  `json:"design"`
	Stock          json.RawMessage `json:"stock"`
	Capability     json.RawMessage `json:"capability"`
	HoleOperations json.RawMessage `json:"hole_operations,omitempty"`
	Bends          json.RawMessage `json:"bends,omitempty"`
}

type designDocument struct {
	Format      dfmconstants.DesignFormat `json:"format"`
	Units       *dfmconstants.LengthUnit  `json:"units,omitempty"`
	Adjustments json.RawMessage           `json:"adjustments,omitempty"`
}

// Document assembles the check-request document. A caller names a capability by sending one or naming a preset, never both.
func Document(in Input) ([]byte, *apierror.APIError) {
	capability := in.Capability
	switch {
	case in.CapabilityPreset != nil && len(in.Capability) > 0:
		return nil, apierror.NewValidationError("Send either `capability` or `capability_preset`, not both.",
			apierror.Field("capability_preset", apierror.CodeInvalidFormat, "Send either `capability` or `capability_preset`, not both."))
	case in.CapabilityPreset != nil:
		data, ok := capabilities.Get(*in.CapabilityPreset)
		if !ok {
			return nil, apierror.NewValidationError("Unknown capability preset.",
				apierror.Field("capability_preset", apierror.CodeInvalidFormat, "No capability preset has this ID. List them with GET /v1/dfm/capability-presets."))
		}
		capability = data
	case len(in.Capability) == 0:
		return nil, apierror.NewValidationError("A capability is required.",
			apierror.Field("capability", apierror.CodeMissingField, "Send a `capability` document or name a `capability_preset`."))
	}
	doc := document{
		Design:         designDocument{Format: in.Format, Units: in.Units, Adjustments: in.Adjustments},
		Stock:          in.Stock,
		Capability:     capability,
		HoleOperations: in.HoleOperations,
		Bends:          in.Bends,
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, apierror.NewInternalError(err, "Failed to assemble the check request.")
	}
	return out, nil
}

// Run checks a design against a check-request document. A design that fails checks is a result; only a request the engine cannot act on is an error.
func Run(ctx context.Context, design, doc []byte) (*dfmresource.Check, *apierror.APIError) {
	req, dfmErr := dfm.RequestFromJSON(design, doc)
	if dfmErr != nil {
		return nil, apierrors.FromEngine(dfmErr)
	}
	res, err := dfm.Check(ctx, req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, apierror.NewRequestTimeoutError("The check did not finish before the request ended.")
		}
		return nil, apierrors.FromEngine(dfm.AsError(err))
	}
	return dfmresource.PresentCheck(res), nil
}

// Preview renders the design as the check read it, as an SVG with the findings marked. It runs the check again, so it costs what Run cost; callers ask for it only when something will show it.
func Preview(ctx context.Context, design, doc []byte) ([]byte, *apierror.APIError) {
	req, dfmErr := dfm.RequestFromJSON(design, doc)
	if dfmErr != nil {
		return nil, apierrors.FromEngine(dfmErr)
	}
	svg, err := dfm.Preview(ctx, req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, apierror.NewRequestTimeoutError("The preview did not finish before the request ended.")
		}
		return nil, apierrors.FromEngine(dfm.AsError(err))
	}
	return svg, nil
}
