package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/open-mrp/dfm-mcp/internal/client"
	dfmconstants "github.com/open-mrp/dfm/constants"

	"github.com/open-mrp/dfm-mcp/internal/constants"
	"github.com/open-mrp/dfm-mcp/internal/localstore"
	"github.com/open-mrp/dfm-mcp/internal/resource"
	"github.com/open-mrp/dfm-mcp/telemetry"
)

// API is the little of the DFM API the local server needs: recording a check, filing feedback, fetching a check that is not local, and uploading a telemetry report. Provision, when set, supplies a key the first time a call needs one, so a machine that never records or reports never gets one.
type API struct {
	Client    *client.Client
	Provision func(ctx context.Context, c *client.Client) error
}

func (a *API) ensureKey(ctx context.Context) error {
	if a.Client.APIKey != "" || a.Provision == nil {
		return nil
	}
	return a.Provision(ctx, a.Client)
}

// RecordCheck stores a locally run check on the server so it has an id there. The drawing is not sent.
func (a *API) RecordCheck(ctx context.Context, e *localstore.Entry) (*resource.DFMCheck, error) {
	if err := a.ensureKey(ctx); err != nil {
		return nil, err
	}
	design := map[string]any{"format": e.Check.Design.Format, "size_bytes": e.Check.Design.SizeBytes, "sha256": e.Check.Design.SHA256}
	if e.Check.Design.Filename != nil {
		design["filename"] = *e.Check.Design.Filename
	}
	body := map[string]any{"request": e.Request, "result": e.Check.Check, "design": design}
	if e.Check.CapabilityPreset != nil {
		body["capability_preset"] = *e.Check.CapabilityPreset
	}
	var out resource.DFMCheck
	if err := a.Client.DoIdempotent(ctx, http.MethodPost, "/v1/dfm/checks/actions/record", nil, body, &out, "record:"+e.Check.ID); err != nil {
		return nil, err
	}
	return &out, nil
}

// UploadDesign puts a drawing in the service's storage ahead of a feedback report that keeps it: a design is created with the file's size and hash, and the file goes to the upload target the service answers with, which is signed for that hash. It returns the design's id.
func (a *API) UploadDesign(ctx context.Context, format dfmconstants.DesignFormat, filename string, data []byte) (string, error) {
	if err := a.ensureKey(ctx); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	body := map[string]any{"format": format, "size_bytes": len(data), "sha256": hex.EncodeToString(sum[:])}
	if filename != "" {
		body["filename"] = filename
	}
	var design resource.Design
	if err := a.Client.Do(ctx, http.MethodPost, "/v1/dfm/designs", nil, body, &design); err != nil {
		return "", err
	}
	if design.Upload == nil {
		return "", fmt.Errorf("the service created design %s without an upload target", design.ID)
	}
	if err := a.Client.Upload(ctx, design.Upload.URL, design.Upload.Headers, data); err != nil {
		return "", fmt.Errorf("uploading the drawing: %w", err)
	}
	return design.ID, nil
}

// GetCheck fetches a check recorded on the server.
func (a *API) GetCheck(ctx context.Context, checkID string) (*resource.DFMCheck, error) {
	if err := a.ensureKey(ctx); err != nil {
		return nil, err
	}
	var out resource.DFMCheck
	if err := a.Client.Do(ctx, http.MethodGet, "/v1/dfm/checks/"+url.PathEscape(checkID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReportFeedback files a report against a recorded check. When the user chose to keep the drawing with it, designID names the drawing uploaded with UploadDesign, or is empty when the check already kept it.
func (a *API) ReportFeedback(ctx context.Context, in FeedbackInput, designID string) (*resource.FeedbackReport, error) {
	if err := a.ensureKey(ctx); err != nil {
		return nil, err
	}
	evidence := map[string]any{"type": in.EvidenceType}
	if in.EvidenceDetail != "" {
		evidence["detail"] = in.EvidenceDetail
	}
	body := map[string]any{"check_id": in.CheckID, "type": in.Type, "reason": in.Reason, "evidence": evidence}
	if in.OccurrenceID != "" {
		body["occurrence_id"] = in.OccurrenceID
	}
	if len(in.Expected) > 0 {
		body["expected"] = json.RawMessage(in.Expected)
	}
	if in.ShareDesign {
		// The check may already keep its drawing, in which case there is nothing to upload and the report only records the consent.
		design := map[string]any{"retention": constants.DesignRetentionRetained}
		if designID != "" {
			design["design_id"] = designID
		}
		body["design"] = design
	}
	var out resource.FeedbackReport
	if err := a.Client.Do(ctx, http.MethodPost, "/v1/dfm/feedback-reports", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UploadTelemetry sends one batch of counts with the batch's own Idempotency-Key, so a batch retried after a lost response is counted once. It needs no API key: the report carries a random installation id and no account.
func (a *API) UploadTelemetry(ctx context.Context, r telemetry.Report, idempotencyKey string) error {
	return a.Client.DoIdempotent(ctx, http.MethodPost, "/v1/dfm/telemetry-reports", nil, r, nil, idempotencyKey)
}

// CreateAPIKey mints a key for this machine, naming this program as the client. attempt identifies this mint, sent as the idempotency key so a mint retried after a lost response gets the same key back instead of a second one; the caller keeps it until the secret is saved and never reuses it afterwards, so a key revoked since is not replayed. The service honors it for key creation without a key of its own.
func (a *API) CreateAPIKey(ctx context.Context, name, attempt string) (*resource.CreatedAPIKey, error) {
	var key resource.CreatedAPIKey
	idempotency := ""
	if attempt != "" {
		idempotency = "mint:" + attempt
	}
	if err := a.Client.DoIdempotent(ctx, http.MethodPost, "/v1/dfm/api-keys", nil, map[string]any{"name": name, "client": map[string]any{"name": "dfm-mcp"}}, &key, idempotency); err != nil {
		return nil, err
	}
	return &key, nil
}
