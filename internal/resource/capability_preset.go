package resource

import (
	"encoding/json"

	"github.com/open-mrp/apikit/example"
	"github.com/open-mrp/apikit/object"
	dfmconstants "github.com/open-mrp/dfm/constants"

	"github.com/open-mrp/dfm-mcp/internal/constants"
)

// A capability preset is a reference capability for a common machine: what a typical fiber laser or press brake of its class can produce. A check names one by its `code` in `capability_preset` to be judged against it.
//
// Presets are a fixed catalog that ships with the engine, so they are identified by a stable code rather than an ID.
type CapabilityPreset struct {
	// Stable code of the preset, the value a check sends as `capability_preset`.
	Code string `json:"code" validate:"required"`
	// Always "dfm_capability_preset".
	Object object.Type `json:"object" validate:"required,enum=dfm_capability_preset"`
	// Process the preset describes.
	Process dfmconstants.Process `json:"process" validate:"required"`
	// Version of the preset; its limits change only with a new version.
	Version int `json:"version" validate:"required"`
	// Machine or class the preset stands for.
	Name string `json:"name" validate:"required"`
	// What the limits rest on and how strong that evidence is.
	Basis string `json:"basis" validate:"required"`
	// Where the limits come from.
	Sources []string `json:"sources" validate:"required"`
	// The capability document itself, as a check would send it as `capability`. Null unless requested with `include[]=capability`.
	Capability json.RawMessage `json:"capability" expandable:"true"`
}

var SampleCapabilityPresetCode = "fiber-3kw-3015"

var SampleCapabilityPreset = &CapabilityPreset{
	Code:    SampleCapabilityPresetCode,
	Object:  constants.ObjectTypeCapabilityPreset,
	Process: dfmconstants.ProcessLaserCutting,
	Version: 1,
	Name:    "Fiber laser, 3 kW, 3015 bed",
	Basis:   "Maker data for a machine of this class (see sources).",
	Sources: []string{"Bystronic ByCut Fiber 3000 technical data, January 2026 (maximum thicknesses)"},
}

func (*CapabilityPreset) SchemaExample() any {
	return example.ValidateAndMarshalToMap(SampleCapabilityPreset)
}
