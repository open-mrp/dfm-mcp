package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/open-mrp/apikit/apierror"
	"github.com/open-mrp/apikit/id"
	"github.com/open-mrp/dfm/capabilities"
	dfmconstants "github.com/open-mrp/dfm/constants"
	dfmresource "github.com/open-mrp/dfm/resource"

	"github.com/open-mrp/dfm-mcp/internal/client"
	"github.com/open-mrp/dfm-mcp/internal/constants"
	"github.com/open-mrp/dfm-mcp/internal/engine"
	"github.com/open-mrp/dfm-mcp/internal/localstore"
	"github.com/open-mrp/dfm-mcp/internal/resource"
	"github.com/open-mrp/dfm-mcp/telemetry"
)

// Local is what the tools run on: the engine in this process, a store of results on this machine, the API for what must live on the server, and the week's counters.
type Local struct {
	// Store (required) keeps the checks run here.
	Store *localstore.Store
	// API (optional; default: nil) records checks, files feedback and uploads telemetry. Without it those are refused with a clear message.
	API *API
	// Counters (optional; default: nil) counts what happens here. Nil counts nothing.
	Counters *telemetry.Counters
}

// CheckInput is a check as the tools assemble it.
type CheckInput struct {
	Design           []byte
	Format           string
	Filename         string
	DesignPath       string
	Units            string
	Record           bool
	Stock            map[string]any
	Capability       json.RawMessage
	CapabilityPreset string
	HoleOperations   json.RawMessage
	Bends            json.RawMessage
}

// FeedbackInput is a report as the tools assemble it. Design is the drawing when the caller gave it; ShareDesign says the user agreed to keep it.
type FeedbackInput struct {
	CheckID        string
	Type           string
	OccurrenceID   string
	Expected       json.RawMessage
	Reason         string
	EvidenceType   string
	EvidenceDetail string
	ShareDesign    bool
	Design         []byte
}

// CheckDesign runs the engine here and keeps the result. With Record, the result (never the drawing) goes to the server at once.
func (l *Local) CheckDesign(ctx context.Context, in CheckInput) (*resource.DFMCheck, error) {
	var units *dfmconstants.LengthUnit
	if in.Units != "" {
		u := dfmconstants.LengthUnit(in.Units)
		units = &u
	}
	var preset *string
	if in.CapabilityPreset != "" {
		preset = &in.CapabilityPreset
	}
	stock, err := json.Marshal(in.Stock)
	if err != nil {
		return nil, err
	}
	doc, apiErr := engine.Document(engine.Input{
		Format: dfmconstants.DesignFormat(in.Format), Units: units,
		Stock: stock, Capability: in.Capability, CapabilityPreset: preset,
		HoleOperations: in.HoleOperations, Bends: in.Bends,
	})
	if apiErr != nil {
		return nil, engineError(apiErr)
	}
	result, apiErr := engine.Run(ctx, in.Design, doc)
	if apiErr != nil {
		l.Counters.Inc(telemetry.CounterErrors)
		return nil, engineError(apiErr)
	}
	checkID, apiErr := id.GenID(constants.CheckIDPrefix, nil)
	if apiErr != nil {
		return nil, engineError(apiErr)
	}
	sum := sha256.Sum256(in.Design)
	var filename *string
	if in.Filename != "" {
		filename = &in.Filename
	}
	check := resource.DFMCheck{
		ID:    checkID,
		Check: *result,
		Design: &resource.CheckDesign{
			Format: dfmconstants.DesignFormat(in.Format), Filename: filename, SizeBytes: int64(len(in.Design)),
			SHA256: hex.EncodeToString(sum[:]), Retention: constants.DesignRetentionNone,
		},
		Stock:            stock,
		CapabilityPreset: preset,
		Origin:           constants.CheckOriginClient,
		CreatedAt:        time.Now().UTC(),
	}
	l.Counters.Inc(telemetry.CounterChecks, telemetry.PrefixVerdict+string(result.Verdict), telemetry.PrefixFormat+in.Format, telemetry.PrefixProcess+string(result.Process))
	entry := &localstore.Entry{Check: check, Request: doc, DesignPath: in.DesignPath}
	if in.Record {
		if l.API == nil {
			return nil, errors.New("this server has no API configured, so the check cannot be recorded; set DFM_API_URL or leave record unset")
		}
		recorded, err := l.API.RecordCheck(ctx, entry)
		if err != nil {
			return nil, fmt.Errorf("the check ran, but recording it on the server failed: %w", err)
		}
		entry.RemoteID = recorded.ID
		l.Counters.Inc(telemetry.CounterChecksRecorded)
	}
	if err := l.Store.Save(entry); err != nil {
		return nil, err
	}
	return &check, nil
}

// GetCheck reads a check run here, or fetches one recorded on the server.
func (l *Local) GetCheck(ctx context.Context, checkID string) (*resource.DFMCheck, error) {
	e, ok, err := l.Store.Load(checkID)
	if err != nil {
		return nil, err
	}
	if ok {
		return &e.Check, nil
	}
	if l.API != nil {
		return l.API.GetCheck(ctx, checkID)
	}
	return nil, &client.Error{Status: http.StatusNotFound, Code: "resource_not_found", Message: "No check with this ID on this machine."}
}

// RecordedAs returns the server's id for a check run here, or "" when it was never recorded or is not local.
func (l *Local) RecordedAs(checkID string) string {
	e, ok, err := l.Store.Load(checkID)
	if err != nil || !ok {
		return ""
	}
	return e.RemoteID
}

// ListPresets lists the engine's reference capabilities, without their documents.
func (l *Local) ListPresets(process string) []resource.CapabilityPreset {
	presets := capabilities.List()
	if process != "" {
		presets = capabilities.ListFor(dfmconstants.Process(process))
	}
	out := make([]resource.CapabilityPreset, 0, len(presets))
	for _, p := range presets {
		out = append(out, presentPreset(p, nil))
	}
	return out
}

// GetPreset returns one reference capability with its document.
func (l *Local) GetPreset(presetID string) (*resource.CapabilityPreset, error) {
	for _, p := range capabilities.List() {
		if p.ID == presetID {
			doc, ok := capabilities.Get(p.ID)
			if !ok {
				return nil, fmt.Errorf("preset %s has no capability document", presetID)
			}
			out := presentPreset(p, doc)
			return &out, nil
		}
	}
	return nil, &client.Error{Status: http.StatusNotFound, Code: "resource_not_found", Message: "No capability preset with this ID. List them with dfm_list_capability_presets."}
}

// FindingCodes is the engine's catalog.
func (l *Local) FindingCodes() []FindingCode {
	out := []FindingCode{}
	for _, c := range dfmresource.PresentCodes().Data {
		sev := make([]string, len(c.Severities))
		for i, s := range c.Severities {
			sev[i] = string(s)
		}
		out = append(out, FindingCode{Code: string(c.Code), Severities: sev, Scope: string(c.Scope), Limit: c.Limit, Description: c.Description, Remediation: c.Remediation})
	}
	return out
}

// ReportFeedback records the check on the server first, if it only exists here, and files the report against the recorded copy. When the user shares the drawing and the server's copy of the check did not keep it, the drawing is uploaded to the service's storage first, as a design of the check's own format whose hash the service matches against the check (read again from where the check found it when it was not sent, and it must still be the same file), and the report names that upload. A check that already kept its drawing needs no upload.
func (l *Local) ReportFeedback(ctx context.Context, in FeedbackInput) (*resource.FeedbackReport, error) {
	if l.API == nil {
		return nil, errors.New("this server has no API configured, so feedback cannot be sent; set DFM_API_URL")
	}
	e, local, err := l.Store.Load(in.CheckID)
	if err != nil {
		return nil, err
	}
	if local {
		if e.RemoteID == "" {
			recorded, err := l.API.RecordCheck(ctx, e)
			if err != nil {
				return nil, fmt.Errorf("recording the check on the server before reporting: %w", err)
			}
			e.RemoteID = recorded.ID
			if err := l.Store.Save(e); err != nil {
				return nil, err
			}
		}
		in.CheckID = e.RemoteID
	}
	designID := ""
	if in.ShareDesign {
		server, err := l.API.GetCheck(ctx, in.CheckID)
		if err != nil {
			return nil, fmt.Errorf("reading the check before sharing its drawing: %w", err)
		}
		if server.Design == nil || server.Design.Retention != constants.DesignRetentionRetained {
			design := in.Design
			if design == nil && local {
				if design, err = readSameDesign(e); err != nil {
					return nil, err
				}
			}
			if design == nil {
				return nil, errors.New("the drawing to share is missing: give file_path or content_base64")
			}
			var format dfmconstants.DesignFormat
			filename := ""
			if server.Design != nil {
				format = server.Design.Format
				if server.Design.Filename != nil {
					filename = *server.Design.Filename
				}
			}
			if designID, err = l.API.UploadDesign(ctx, format, filename, design); err != nil {
				return nil, err
			}
		}
	}
	out, err := l.API.ReportFeedback(ctx, in, designID)
	if err != nil {
		return nil, err
	}
	l.Counters.Inc(telemetry.CounterFeedback)
	return out, nil
}

// readSameDesign reads the drawing a check ran on, from where it was, and checks it has not changed since.
func readSameDesign(e *localstore.Entry) ([]byte, error) {
	if e.DesignPath == "" {
		return nil, errors.New("the drawing was given inline when the check ran, so send it again as content_base64 to share it")
	}
	data, err := os.ReadFile(e.DesignPath) // #nosec G304 - the file the check itself was given
	if err != nil {
		return nil, fmt.Errorf("the drawing at %s cannot be read any more; send it as file_path or content_base64: %w", e.DesignPath, err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != e.Check.Design.SHA256 {
		return nil, fmt.Errorf("the drawing at %s has changed since the check ran; send the file the check ran on as content_base64", e.DesignPath)
	}
	return data, nil
}

func presentPreset(p capabilities.Preset, doc []byte) resource.CapabilityPreset {
	sources := p.Sources
	if sources == nil {
		sources = []string{}
	}
	return resource.CapabilityPreset{
		Code: p.ID, Object: constants.ObjectTypeCapabilityPreset, Process: p.Process, Version: p.Version,
		Name: p.Title, Basis: p.Notes, Sources: sources, Capability: json.RawMessage(doc),
	}
}

// engineError presents a kit error from the in-process engine the way the API would, so the agent sees one shape either way.
func engineError(apiErr *apierror.APIError) error {
	e := &client.Error{Status: apiErr.Code.Status(), Code: string(apiErr.Code), Message: apiErr.PublicMessage}
	if apiErr.Param != "" {
		param := apiErr.Param
		e.Param = &param
	}
	if len(apiErr.Errors) > 0 {
		fields := make([]map[string]string, 0, len(apiErr.Errors))
		for _, fe := range apiErr.Errors {
			fields = append(fields, map[string]string{"param": fe.Param, "code": string(fe.Code), "message": fe.Message})
		}
		e.Errors, _ = json.Marshal(fields)
	}
	return e
}
