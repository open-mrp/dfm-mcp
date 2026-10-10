// Package mcpserver is the MCP server over the DFM API: a small set of task-shaped tools, the catalogs as resources, and a prompt for the common workflow. It holds no engine of its own; every tool is one API call with the session's key.
//
// The design follows the MCP specification's tool guidance and the practice of writing tools for agents: few tools named for what they do, descriptions written for a new user, concise results by default with a way to ask for everything, errors an agent can act on, and the user's consent, through elicitation where the client supports it, before a drawing is kept.
package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	dfmconstants "github.com/open-mrp/dfm/constants"
	dfmresource "github.com/open-mrp/dfm/resource"

	"github.com/open-mrp/dfm-mcp/internal/client"
	"github.com/open-mrp/dfm-mcp/internal/constants"
	"github.com/open-mrp/dfm-mcp/internal/resource"
)

// Version is the server's version, stamped at build time.
var Version = "dev"

// Options configures the server.
type Options struct {
	// Local (required) is the engine, the store and the API behind the tools.
	Local *Local
}

const instructions = `DFM (design for manufacturing) checks for sheet-metal drawings: DXF, SVG and STEP. The engine answers "can this part be made on this machine from this stock", listing every problem by part with where it is and how to fix it.

This server runs the engine on this machine: a drawing never leaves it unless the user shares it with a feedback report, or asks for a check to be recorded (which sends the result, never the drawing).

Workflow: pick a machine with dfm_list_capability_presets (or read dfm://capability-presets), then call dfm_check_design with the drawing, material_code and thickness. Read the verdict and the issues; the remediation on each issue says what to change. For every occurrence, call dfm_get_check with response_format "detailed" or read dfm://checks/{id}. dfm_list_finding_codes explains any code.

Drawings are often proprietary. Nothing is sent anywhere unless asked: set record or share_design only after the user agrees, and say so when you do.

When a result looks wrong, use dfm_report_feedback with what you know; people review reports and fix the engine.`

// New builds the MCP server for one session.
func New(opts Options) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "dfm", Title: "DFM check engine", Version: Version}, &mcp.ServerOptions{
		Instructions: instructions,
		// Usage counts carry the client the agent runs in (claude-code, cursor, ...), as it named itself; nothing else about it.
		InitializedHandler: func(_ context.Context, req *mcp.InitializedRequest) {
			if p := req.Session.InitializeParams(); p != nil && p.ClientInfo != nil && opts.Local != nil {
				opts.Local.Counters.SetClient(p.ClientInfo.Name, p.ClientInfo.Version)
			}
		},
	})
	addCatalogTools(s, opts.Local)
	addCheckTools(s, opts.Local)
	addFeedbackTool(s, opts.Local)
	addResources(s, opts.Local)
	addPrompts(s)
	return s
}

// --- Tools ---

func addCatalogTools(s *mcp.Server, b *Local) {

	presetsSchema := mustSchema[ListPresetsInput]()
	presetsSchema.Properties["process"].Enum = enumValues(dfmconstants.Process(""))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "dfm_list_capability_presets",
		Description: "Lists the reference capabilities: common laser cutters and press brakes a design can be checked against by passing the preset's id as capability_preset. Use dfm_get_capability_preset for a preset's full limits.",
		InputSchema: presetsSchema,
		Annotations: &mcp.ToolAnnotations{Title: "List capability presets", ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in ListPresetsInput) (*mcp.CallToolResult, PresetList, error) {
		presets := b.ListPresets(in.Process)
		out := PresetList{Presets: make([]PresetSummary, 0, len(presets))}
		var text strings.Builder
		for _, p := range presets {
			out.Presets = append(out.Presets, PresetSummary{Code: p.Code, Process: p.Process, Name: p.Name, Basis: p.Basis})
			fmt.Fprintf(&text, "- %s (%s): %s\n", p.Code, p.Process, p.Name)
		}
		return textResult(text.String()), out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:         "dfm_get_capability_preset",
		Description:  "Retrieves a capability preset with its full capability document: the machine's limits and the materials and thicknesses it handles. Edit the document and pass it as capability to check against a machine that differs from the preset.",
		OutputSchema: mustSchema[resource.CapabilityPreset](),
		Annotations:  &mcp.ToolAnnotations{Title: "Get a capability preset", ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in GetPresetInput) (*mcp.CallToolResult, *resource.CapabilityPreset, error) {
		out, err := b.GetPreset(in.Code)
		if err != nil {
			return nil, nil, toolError(err)
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "dfm_list_finding_codes",
		Description: "Explains finding codes: what each problem a check can report means, how severe it can be, which capability limit drives it and how to fix it. Pass codes to explain only those; omit them for the whole catalog.",
		Annotations: &mcp.ToolAnnotations{Title: "Explain finding codes", ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in ListFindingCodesInput) (*mcp.CallToolResult, FindingCodeList, error) {
		codes := b.FindingCodes()
		wanted := map[string]bool{}
		for _, code := range in.Codes {
			wanted[code] = true
		}
		out := FindingCodeList{Codes: []FindingCode{}}
		var text strings.Builder
		for _, fc := range codes {
			if len(wanted) > 0 && !wanted[fc.Code] {
				continue
			}
			out.Codes = append(out.Codes, fc)
			fmt.Fprintf(&text, "- %s (%s): %s Fix: %s\n", fc.Code, strings.Join(fc.Severities, "/"), fc.Description, fc.Remediation)
		}
		if len(wanted) > 0 && len(out.Codes) < len(wanted) {
			for code := range wanted {
				if !containsCode(out.Codes, code) {
					fmt.Fprintf(&text, "- %s: not a finding code this engine reports\n", code)
				}
			}
		}
		return textResult(text.String()), out, nil
	})
}

func addCheckTools(s *mcp.Server, b *Local) {

	checkSchema := mustSchema[CheckDesignInput]()
	checkSchema.Properties["format"].Enum = enumValues(dfmconstants.DesignFormat(""))
	checkSchema.Properties["units"].Enum = enumValues(dfmconstants.LengthUnit(""))
	checkSchema.Properties["thickness_unit"].Enum = enumValues(dfmconstants.LengthUnit(""))
	checkSchema.Properties["response_format"].Enum = []any{"concise", "detailed"}
	mcp.AddTool(s, &mcp.Tool{
		Name: "dfm_check_design",
		Description: "Checks a sheet-metal drawing against a machine's capability and a stock, and reports whether the part can be made and every problem found, by part, with where it is and how to fix it. " +
			"Give the drawing as file_path or content_base64, the material_code and thickness of the stock, and either a capability_preset id (from dfm_list_capability_presets) or a capability document. " +
			"The default concise result lists each issue once with how often and where it occurs; ask for detailed when every occurrence matters. " +
			"The check runs on this machine and the drawing stays here; with record, which the user must have agreed to, the result (never the drawing) is recorded on the DFM service.",
		InputSchema:  checkSchema,
		OutputSchema: mustSchema[CheckOutput](),
		Annotations:  &mcp.ToolAnnotations{Title: "Check a design", DestructiveHint: ptr(false), OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CheckDesignInput) (*mcp.CallToolResult, *CheckOutput, error) {
		content, format, filename, err := readDesign(in.FilePath, in.ContentBase64, in.Format, true)
		if err != nil {
			return nil, nil, err
		}
		stock := map[string]any{"material_code": in.MaterialCode}
		if in.Thickness != "" {
			unit := in.ThicknessUnit
			if unit == "" {
				unit = "mm"
			}
			stock["thickness"] = map[string]string{"value": in.Thickness, "unit": unit}
		}
		check, err := b.CheckDesign(ctx, CheckInput{
			Design: content, Format: format, Filename: filename, DesignPath: in.FilePath, Units: in.Units, Record: in.Record,
			Stock: stock, Capability: in.Capability, CapabilityPreset: in.CapabilityPreset, HoleOperations: in.HoleOperations, Bends: in.Bends,
		})
		if err != nil {
			return nil, nil, toolError(err)
		}
		return checkResult(check, in.ResponseFormat, b.RecordedAs(check.ID))
	})

	getSchema := mustSchema[GetCheckInput]()
	getSchema.Properties["response_format"].Enum = []any{"concise", "detailed"}
	mcp.AddTool(s, &mcp.Tool{
		Name:         "dfm_get_check",
		Description:  "Retrieves a check made with this key by its id, as a concise summary or, with response_format detailed, the complete result with every occurrence's location and entities.",
		InputSchema:  getSchema,
		OutputSchema: mustSchema[CheckOutput](),
		Annotations:  &mcp.ToolAnnotations{Title: "Get a check", ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetCheckInput) (*mcp.CallToolResult, *CheckOutput, error) {
		check, err := b.GetCheck(ctx, in.CheckID)
		if err != nil {
			return nil, nil, toolError(err)
		}
		return checkResult(check, in.ResponseFormat, b.RecordedAs(check.ID))
	})
}

func addFeedbackTool(s *mcp.Server, b *Local) {
	schema := mustSchema[ReportFeedbackInput]()
	schema.Properties["type"].Enum = enumValues(constants.FeedbackReportType(""))
	schema.Properties["evidence_type"].Enum = enumValues(constants.EvidenceType(""))
	mcp.AddTool(s, &mcp.Tool{
		Name: "dfm_report_feedback",
		Description: "Reports that a check's result was wrong: a finding that should not be there (false_positive, with its occurrence_id), one that is missing (false_negative, with the expected code and where) or a measurement that is off (wrong_measurement, with the occurrence_id and the correct actual). " +
			"Say why and what the evidence is. People review reports and fix the engine. " +
			"A report is far more useful with the drawing; if the user agrees, set share_design and, when the check did not keep the drawing, give the same file again. Where the client supports it, the user is asked directly.",
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{Title: "Report a wrong result", DestructiveHint: ptr(false), OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in ReportFeedbackInput) (*mcp.CallToolResult, *resource.FeedbackReport, error) {
		share := in.ShareDesign
		if !share && in.FilePath == "" && in.ContentBase64 == "" {
			answer, ask := shareDesignDecision(req)
			if ask != nil {
				return ask, nil, nil
			}
			share = answer
		}
		fb := FeedbackInput{
			CheckID: in.CheckID, Type: in.Type, OccurrenceID: in.OccurrenceID, Expected: in.Expected,
			Reason: in.Reason, EvidenceType: in.EvidenceType, EvidenceDetail: in.EvidenceDetail, ShareDesign: share,
		}
		if share && (in.FilePath != "" || in.ContentBase64 != "") {
			content, _, _, err := readDesign(in.FilePath, in.ContentBase64, "", false)
			if err != nil {
				return nil, nil, err
			}
			fb.Design = content
		}
		out, err := b.ReportFeedback(ctx, fb)
		if err != nil {
			return nil, nil, toolError(err)
		}
		text := fmt.Sprintf("Recorded feedback %s on check %s (%s). Design kept with the report: %s.", out.ID, in.CheckID, out.Type, out.Design.Retention)
		return textResult(text), out, nil
	})
}

// shareDesignDecision asks the user, through the client, whether the drawing may be kept with the report, as a multi round-trip request: the first call returns the question and the SDK brings the answer back on the retry (for older clients it asks through the legacy elicitation call and re-invokes the handler itself). Only clients that support form elicitation are asked; anyone else is taken to have said no, since the tool's description already tells the agent to ask.
func shareDesignDecision(req *mcp.CallToolRequest) (share bool, ask *mcp.CallToolResult) {
	if res, ok := req.Params.InputResponses[shareDesignInput].(*mcp.ElicitResult); ok {
		if res.Action != "accept" {
			return false, nil
		}
		v, _ := res.Content[shareDesignInput].(bool)
		return v, nil
	}
	if !supportsFormElicitation(req) {
		return false, nil
	}
	return false, &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{
			shareDesignInput: &mcp.ElicitParams{
				Mode:    "form",
				Message: "May the DFM service keep this drawing with the feedback report? It stays private and encrypted, and is used only to reproduce the problem and improve the engine.",
				RequestedSchema: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						shareDesignInput: {Type: "boolean", Title: "Keep the drawing with the report", Default: json.RawMessage("false")},
					},
					Required: []string{shareDesignInput},
				},
			},
		},
	}
}

// shareDesignInput names the consent question in input requests and the field in its answer.
const shareDesignInput = "share_design"

func supportsFormElicitation(req *mcp.CallToolRequest) bool {
	caps := req.ClientCapabilities()
	if (caps == nil || caps.Elicitation == nil) && req.Session != nil && req.Session.InitializeParams() != nil {
		// Older clients declare their capabilities once, at initialization, rather than on each request.
		caps = req.Session.InitializeParams().Capabilities
	}
	if caps == nil || caps.Elicitation == nil {
		return false
	}
	return caps.Elicitation.Form != nil || caps.Elicitation.URL == nil
}

// --- Resources ---

func addResources(s *mcp.Server, b *Local) {
	s.AddResource(&mcp.Resource{
		URI:         "dfm://finding-codes",
		Name:        "finding-codes",
		Title:       "Finding codes",
		Description: "Every problem a check can report: meaning, severities, the capability limit behind it and how to fix it.",
		MIMEType:    "application/json",
	}, jsonResource(func(_ context.Context, _ string) (any, error) {
		return FindingCodeList{Codes: b.FindingCodes()}, nil
	}))
	s.AddResource(&mcp.Resource{
		URI:         "dfm://capability-presets",
		Name:        "capability-presets",
		Title:       "Capability presets",
		Description: "Reference capabilities for common laser cutters and press brakes.",
		MIMEType:    "application/json",
	}, jsonResource(func(_ context.Context, _ string) (any, error) {
		return b.ListPresets(""), nil
	}))
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "dfm://capability-presets/{code}",
		Name:        "capability-preset",
		Title:       "Capability preset",
		Description: "One preset with its full capability document.",
		MIMEType:    "application/json",
	}, jsonResource(func(_ context.Context, uri string) (any, error) {
		id, ok := strings.CutPrefix(uri, "dfm://capability-presets/")
		if !ok || id == "" {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		return b.GetPreset(id)
	}))
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "dfm://checks/{id}",
		Name:        "check",
		Title:       "Check result",
		Description: "The complete result of a check, every occurrence included.",
		MIMEType:    "application/json",
	}, jsonResource(func(ctx context.Context, uri string) (any, error) {
		id, ok := strings.CutPrefix(uri, "dfm://checks/")
		if !ok || id == "" {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		return b.GetCheck(ctx, id)
	}))
}

// jsonResource serves a resource from a value the backend provides. A missing record is a resource-not-found error, never an empty result.
func jsonResource(load func(ctx context.Context, uri string) (any, error)) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		v, err := load(ctx, req.Params.URI)
		if err != nil {
			var apiErr *client.Error
			if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
				return nil, mcp.ResourceNotFoundError(req.Params.URI)
			}
			return nil, err
		}
		data, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "application/json", Text: string(data)}}}, nil
	}
}

// --- Prompts ---

func addPrompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        "dfm_review_drawing",
		Title:       "Review a drawing for manufacturability",
		Description: "Walks through checking a drawing: choose a machine, run the check, explain the findings and what to change.",
		Arguments: []*mcp.PromptArgument{
			{Name: "drawing", Description: "Path or name of the drawing to check", Required: true},
			{Name: "material", Description: "Material code, such as mild_steel or aluminum_5052", Required: true},
			{Name: "thickness_mm", Description: "Sheet thickness in millimeters", Required: true},
			{Name: "machine", Description: "A capability preset id, or a description of the shop's machine", Required: false},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		a := req.Params.Arguments
		machine := a["machine"]
		if machine == "" {
			machine = "the most representative preset from dfm_list_capability_presets"
		}
		text := fmt.Sprintf("Review the drawing %s for manufacturability as %s, %s mm thick, on %s.\n\n"+
			"1. Choose the capability: list the presets if you do not have an id, and say which you chose and why.\n"+
			"2. Run dfm_check_design. Do not retain the design unless I say so.\n"+
			"3. Report the verdict, then each issue by part: what it is, how many places, what the limit is, and the concrete change to the drawing that fixes it. Use dfm_list_finding_codes for any code you cannot explain.\n"+
			"4. List the checks that were skipped and what the capability would need for them to run.\n"+
			"5. If a finding seems wrong for this material or machine, say so and offer to report it with dfm_report_feedback.",
			a["drawing"], a["material"], a["thickness_mm"], machine)
		return &mcp.GetPromptResult{
			Description: "Manufacturability review of " + a["drawing"],
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
		}, nil
	})
}

// --- Inputs and outputs ---

// CheckDesignInput is dfm_check_design's input.
type CheckDesignInput struct {
	FilePath         string          `json:"file_path,omitempty" jsonschema:"Path of the drawing on this machine (DXF, SVG or STEP). Prefer this: the file is read here and never uploaded. Use this or content_base64."`
	ContentBase64    string          `json:"content_base64,omitempty" jsonschema:"The drawing's bytes, base64 encoded. Use this or file_path."`
	Format           string          `json:"format,omitempty" jsonschema:"Format of the drawing. Inferred from the file name when omitted."`
	Units            string          `json:"units,omitempty" jsonschema:"Drawing units, only when the file declares none (SVG in pixels, some DXF)."`
	MaterialCode     string          `json:"material_code" jsonschema:"Material code the capability lists, such as mild_steel, stainless_304 or aluminum_5052."`
	Thickness        string          `json:"thickness" jsonschema:"Sheet thickness as a decimal string, such as 1.5."`
	ThicknessUnit    string          `json:"thickness_unit,omitempty" jsonschema:"Unit of thickness; mm when omitted."`
	CapabilityPreset string          `json:"capability_preset,omitempty" jsonschema:"Id of a capability preset from dfm_list_capability_presets. Use this or capability."`
	Capability       json.RawMessage `json:"capability,omitempty" jsonschema:"A capability document of your own, as dfm_get_capability_preset returns one. Use this or capability_preset."`
	HoleOperations   json.RawMessage `json:"hole_operations,omitempty" jsonschema:"Operations (taps, countersinks, press-in fasteners) assigned to round holes, from the capability's hole_operations."`
	Bends            json.RawMessage `json:"bends,omitempty" jsonschema:"Angle, direction and inside radius of bend lines, for a bending check; unlisted bend lines are 90 degrees up."`
	Record           bool            `json:"record,omitempty" jsonschema:"Record the result on the DFM service right away (the result and the drawing's name, size and hash, never the drawing), so the check is counted and can be retrieved anywhere. Only with the user's consent; the default records nothing until feedback is reported."`
	ResponseFormat   string          `json:"response_format,omitempty" jsonschema:"concise (default): each issue once with its count and first locations. detailed: the complete result with every occurrence."`
}

// GetCheckInput is dfm_get_check's input.
type GetCheckInput struct {
	CheckID        string `json:"check_id" jsonschema:"The check's id, such as dfck_3g20lmq8eflb."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"concise (default) or detailed."`
}

// ListPresetsInput is dfm_list_capability_presets's input.
type ListPresetsInput struct {
	Process string `json:"process,omitempty" jsonschema:"Only presets for this process."`
}

// GetPresetInput is dfm_get_capability_preset's input.
type GetPresetInput struct {
	Code string `json:"code" jsonschema:"The preset's code, such as fiber-3kw-3015."`
}

// ListFindingCodesInput is dfm_list_finding_codes's input.
type ListFindingCodesInput struct {
	Codes []string `json:"codes,omitempty" jsonschema:"Finding codes to explain; omit for the whole catalog."`
}

// ReportFeedbackInput is dfm_report_feedback's input.
type ReportFeedbackInput struct {
	CheckID        string          `json:"check_id" jsonschema:"The check whose result is wrong."`
	Type           string          `json:"type" jsonschema:"What kind of mistake it is."`
	OccurrenceID   string          `json:"occurrence_id,omitempty" jsonschema:"Id of the wrong occurrence, from the result's issues; required for false_positive and wrong_measurement."`
	Expected       json.RawMessage `json:"expected,omitempty" jsonschema:"For false_negative: {code, entity_ids or location {x, y} in mm}. For wrong_measurement: {code, actual {value, unit}}."`
	Reason         string          `json:"reason" jsonschema:"Why the result is wrong, in plain words."`
	EvidenceType   string          `json:"evidence_type" jsonschema:"How the reporter knows."`
	EvidenceDetail string          `json:"evidence_detail,omitempty" jsonschema:"Who, when, an order or message reference."`
	ShareDesign    bool            `json:"share_design,omitempty" jsonschema:"Keep the drawing with the report. Only with the user's consent."`
	FilePath       string          `json:"file_path,omitempty" jsonschema:"Path of the drawing the check ran on, when sharing it and the check did not keep it."`
	ContentBase64  string          `json:"content_base64,omitempty" jsonschema:"The drawing's bytes, base64 encoded, when sharing it and the check did not keep it."`
}

// PresetList is dfm_list_capability_presets's output.
type PresetList struct {
	Presets []PresetSummary `json:"presets"`
}

// PresetSummary is one preset without its document.
type PresetSummary struct {
	Code    string               `json:"code" jsonschema:"The value a check sends as capability_preset."`
	Process dfmconstants.Process `json:"process"`
	Name    string               `json:"name"`
	Basis   string               `json:"basis" jsonschema:"What the limits rest on and how strong that evidence is."`
}

// FindingCodeList is dfm_list_finding_codes's output.
type FindingCodeList struct {
	Codes []FindingCode `json:"codes"`
}

// FindingCode is one entry of the catalog.
type FindingCode struct {
	Code        string   `json:"code"`
	Severities  []string `json:"severities"`
	Scope       string   `json:"scope"`
	Limit       *string  `json:"limit"`
	Description string   `json:"description"`
	Remediation string   `json:"remediation"`
}

// CheckOutput is what the check tools return: a summary always, and the full result when asked for.
type CheckOutput struct {
	CheckID       string               `json:"check_id"`
	Verdict       string               `json:"verdict" jsonschema:"manufacturable, needs_review or not_manufacturable."`
	EngineVersion string               `json:"engine_version"`
	ErrorCount    int                  `json:"error_count"`
	ReviewCount   int                  `json:"review_count"`
	WarningCount  int                  `json:"warning_count"`
	PartCount     int                  `json:"part_count"`
	Parts         []PartSummary        `json:"parts"`
	DrawingIssues []IssueSummary       `json:"drawing_issues"`
	SkippedChecks []string             `json:"skipped_checks" jsonschema:"Codes of checks that did not run because the capability sets no limit for them."`
	Origin        string               `json:"origin" jsonschema:"server when the API ran the check, client when this server ran the engine itself."`
	RecordedAs    *string              `json:"recorded_as" jsonschema:"The check's id on the DFM service once it has been recorded there (by record or by a feedback report), or null while it exists only on this machine."`
	Design        resource.CheckDesign `json:"design"`
	Result        *resource.DFMCheck   `json:"result,omitempty" jsonschema:"The complete result, only with response_format detailed."`
}

// PartSummary is one part of the drawing.
type PartSummary struct {
	Number    int            `json:"number"`
	CopyCount int            `json:"copy_count" jsonschema:"How many copies of this part the drawing calls for."`
	Verdict   string         `json:"verdict"`
	Size      string         `json:"size,omitempty" jsonschema:"Width by height, with units."`
	Issues    []IssueSummary `json:"issues"`
}

// IssueSummary is one problem, with how often and where it occurs.
type IssueSummary struct {
	Code        string   `json:"code"`
	Severity    string   `json:"severity"`
	Message     string   `json:"message"`
	Remediation string   `json:"remediation"`
	Required    string   `json:"required,omitempty" jsonschema:"The limit that was not met."`
	Occurrences int      `json:"occurrences"`
	Examples    []string `json:"examples" jsonschema:"The first occurrences: id, measured value and location."`
}

// maxExamples bounds how many occurrences a concise issue lists; the detailed result has them all.
const maxExamples = 3

func checkResult(check *resource.DFMCheck, format string, recordedAs string) (*mcp.CallToolResult, *CheckOutput, error) {
	out := &CheckOutput{
		CheckID:       check.ID,
		Verdict:       string(check.Verdict),
		EngineVersion: check.EngineVersion,
		ErrorCount:    check.ErrorCount,
		ReviewCount:   check.ReviewCount,
		WarningCount:  check.WarningCount,
		PartCount:     check.PartCount,
		Origin:        string(check.Origin),
		Parts:         []PartSummary{},
		DrawingIssues: []IssueSummary{},
		SkippedChecks: []string{},
	}
	if recordedAs != "" && recordedAs != check.ID {
		out.RecordedAs = &recordedAs
	}
	if check.Design != nil {
		out.Design = *check.Design
	}
	if check.Parts != nil {
		for _, p := range check.Parts.Data {
			ps := PartSummary{Number: p.Number, CopyCount: p.CopyCount, Verdict: string(p.Verdict), Issues: []IssueSummary{}}
			if p.Sheet != nil && p.Sheet.Width != nil && p.Sheet.Height != nil {
				ps.Size = quantity(p.Sheet.Width) + " x " + quantity(p.Sheet.Height)
			}
			if p.Issues != nil {
				for _, is := range p.Issues.Data {
					ps.Issues = append(ps.Issues, summarizeIssue(is))
				}
			}
			out.Parts = append(out.Parts, ps)
		}
	}
	if check.DrawingIssues != nil {
		for _, is := range check.DrawingIssues.Data {
			out.DrawingIssues = append(out.DrawingIssues, summarizeIssue(is))
		}
	}
	if check.SkippedChecks != nil {
		for _, sc := range check.SkippedChecks.Data {
			out.SkippedChecks = append(out.SkippedChecks, string(sc.Code))
		}
	}
	if format == "detailed" {
		out.Result = check
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Check %s: %s (%d errors, %d for review, %d warnings; %d part(s); engine %s).\n", check.ID, check.Verdict, check.ErrorCount, check.ReviewCount, check.WarningCount, check.PartCount, check.EngineVersion)
	for _, p := range out.Parts {
		fmt.Fprintf(&b, "Part %d", p.Number)
		if p.CopyCount > 1 {
			fmt.Fprintf(&b, " (x%d)", p.CopyCount)
		}
		if p.Size != "" {
			fmt.Fprintf(&b, ", %s", p.Size)
		}
		fmt.Fprintf(&b, ": %s\n", p.Verdict)
		writeIssues(&b, p.Issues)
	}
	if len(out.DrawingIssues) > 0 {
		b.WriteString("Drawing as a whole:\n")
		writeIssues(&b, out.DrawingIssues)
	}
	if len(out.SkippedChecks) > 0 {
		fmt.Fprintf(&b, "Skipped (no limit in the capability): %s\n", strings.Join(out.SkippedChecks, ", "))
	}
	if out.RecordedAs != nil {
		fmt.Fprintf(&b, "Recorded on the DFM service as %s.\n", *out.RecordedAs)
	}
	if format != "detailed" {
		fmt.Fprintf(&b, "Every occurrence: dfm_get_check with response_format detailed, or resource dfm://checks/%s.\n", check.ID)
	}
	res := textResult(b.String())
	res.Content = append(res.Content, &mcp.ResourceLink{
		URI: "dfm://checks/" + check.ID, Name: check.ID, Title: "Complete check result", MIMEType: "application/json",
	})
	return res, out, nil
}

// quantity formats a value with its unit the way a person would read it. The engine stops formatting for clients, so this is the one place the server does.
func quantity(q *dfmresource.Quantity) string {
	if q.Unit == dfmconstants.UnitPercent {
		return q.Value + "%"
	}
	return q.Value + " " + string(q.Unit)
}

func summarizeIssue(is dfmresource.Issue) IssueSummary {
	out := IssueSummary{
		Code: string(is.Code), Severity: string(is.Severity), Message: is.Message, Remediation: is.Remediation, Examples: []string{},
	}
	if is.Required != nil {
		out.Required = quantity(is.Required)
	}
	if is.Occurrences != nil {
		out.Occurrences = len(is.Occurrences.Data)
		for i, o := range is.Occurrences.Data {
			if i == maxExamples {
				break
			}
			ex := o.ID
			if o.Actual != nil {
				ex += " measured " + quantity(o.Actual)
			}
			if o.Location != nil {
				ex += fmt.Sprintf(" at (%s, %s) %s", o.Location.X, o.Location.Y, o.Location.Unit)
			}
			if len(o.EntityIDs) > 0 {
				ex += " entities " + strings.Join(o.EntityIDs, ",")
			}
			out.Examples = append(out.Examples, ex)
		}
	}
	return out
}

func writeIssues(b *strings.Builder, issues []IssueSummary) {
	for _, is := range issues {
		fmt.Fprintf(b, "  - [%s] %s x%d: %s", is.Severity, is.Code, is.Occurrences, is.Message)
		if is.Required != "" {
			fmt.Fprintf(b, " (limit %s)", is.Required)
		}
		fmt.Fprintf(b, " Fix: %s\n", is.Remediation)
		for _, ex := range is.Examples {
			fmt.Fprintf(b, "      %s\n", ex)
		}
	}
}

// --- Helpers ---

// readDesign returns the drawing's bytes, format and file name from a path or inline content. The format is inferred from the file name when needed and not given.
func readDesign(path, contentBase64, format string, needFormat bool) ([]byte, string, string, error) {
	var content []byte
	var filename string
	switch {
	case path != "" && contentBase64 != "":
		return nil, "", "", fmt.Errorf("send either file_path or content_base64, not both")
	case path != "":
		data, err := os.ReadFile(path) // #nosec G304 - the agent names a file on its own machine
		if err != nil {
			return nil, "", "", fmt.Errorf("reading %s: %w", path, err)
		}
		content, filename = data, filepath.Base(path)
	case contentBase64 != "":
		data, err := base64.StdEncoding.DecodeString(contentBase64)
		if err != nil {
			return nil, "", "", fmt.Errorf("content_base64 is not valid base64 (standard alphabet, with padding): %w", err)
		}
		content = data
	default:
		return nil, "", "", fmt.Errorf("the drawing is missing: send file_path or content_base64")
	}
	if format == "" && needFormat {
		switch strings.ToLower(filepath.Ext(filename)) {
		case ".dxf":
			format = string(dfmconstants.DesignFormatDXF)
		case ".svg":
			format = string(dfmconstants.DesignFormatSVG)
		case ".step", ".stp":
			format = string(dfmconstants.DesignFormatSTEP)
		default:
			return nil, "", "", fmt.Errorf("format is required when it cannot be told from the file name: dxf, svg or step")
		}
	}
	return content, format, filename, nil
}

// toolError turns an API error into one an agent can act on: the code, the message, the parameter at fault and every failing field, plus what to do about the common cases.
func toolError(err error) error {
	var apiErr *client.Error
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("the DFM API could not be reached: %w", err)
	}
	msg := apiErr.Error()
	switch apiErr.Code {
	case "invalid_credentials":
		msg += ". The saved API key is not valid any more: remove it from ~/.config/dfm/credentials.json (or unset DFM_API_KEY) and the server will make a new one."
	case "rate_limited":
		msg += ". Wait before retrying."
	}
	return errors.New(msg)
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// mustSchema infers a schema from a Go type. A json.RawMessage field holds any JSON value (a stock, a capability document), which the default inference would describe as an array of bytes.
func mustSchema[T any]() *jsonschema.Schema {
	s, err := jsonschema.For[T](&jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[json.RawMessage](): {},
	}})
	if err != nil {
		panic(err)
	}
	return s
}

func enumValues(e interface{ EnumValues() []string }) []any {
	vals := e.EnumValues()
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = v
	}
	return out
}

func containsCode(codes []FindingCode, code string) bool {
	for _, c := range codes {
		if c.Code == code {
			return true
		}
	}
	return false
}

func ptr[T any](v T) *T { return &v }
