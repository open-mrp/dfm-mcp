// Package resource holds the API's response resources and the sample values its reference documents them with.
package resource

import (
	"time"

	"github.com/open-mrp/apikit/example"
	"github.com/open-mrp/apikit/object"

	"github.com/open-mrp/dfm-mcp/internal/constants"
)

// An API key identifies a caller. Keys are self-served: anyone can create one, and it is the only credential the API knows. A key acts only for itself: it sees the checks, designs, jobs and reports it created.
type APIKey struct {
	// API key ID.
	ID string `json:"id" validate:"required"`
	// Always "dfm_api_key".
	Object object.Type `json:"object" validate:"required,enum=dfm_api_key"`
	// Name the caller gave the key, to tell it apart from others, or null.
	Name *string `json:"name"`
	// Contact email the caller chose to leave, or null.
	Email *string `json:"email"`
	// The software that uses the key, as the caller described it when creating the key, or null. It attributes checks and reports to the agent or integration that made them.
	Client *APIKeyClient `json:"client"`
	// The secret, redacted for display: its prefix and last four characters, such as `dfm_sk_****k2m9`. Enough to recognize the key in a configuration, useless on its own.
	RedactedValue string `json:"redacted_value" validate:"required"`
	// Whether the key still authenticates requests. Read-only: a key is revoked with its revoke or rotate action.
	Status constants.APIKeyStatus `json:"status" validate:"required"`
	// When the key last authenticated a request. Recorded at most once every five minutes, so it can lag the key's latest use by that much.
	LastUsedAt *time.Time `json:"last_used_at"`
	// When the key stopped authenticating requests, or null while it works. A revoked key keeps its record, so what it created stays retrievable with its replacement.
	RevokedAt *time.Time `json:"revoked_at"`
	// When the key was created.
	CreatedAt time.Time `json:"created_at" validate:"required"`
	// When the key's details last changed.
	UpdatedAt time.Time `json:"updated_at" validate:"required"`
}

// APIKeyClient names the software that uses a key.
type APIKeyClient struct {
	// The program, such as `claude-code`, `cursor` or `dfm-mcp`.
	Name string `json:"name" validate:"required"`
	// The agent's source: where the agent came from, such as `docs`, `mcp_registry` or a referring site, or null.
	Source *string `json:"source"`
}

// A newly issued API key with its secret, returned when a key is created or rotated.
type CreatedAPIKey struct {
	// Always "dfm_created_api_key".
	Object object.Type `json:"object" validate:"required,enum=dfm_created_api_key"`
	// The secret, sent on every other request as `Authorization: Bearer <secret>`. This is the only response that ever carries it; a lost secret is replaced by rotating the key.
	Secret string `json:"secret" validate:"required" sensitive:"true"`
	// The key's details without the secret.
	APIKey APIKey `json:"api_key" validate:"required"`
}

const SampleAPIKeyID = "apke_of6pr7ox32l0" // #nosec G101 - a sample ID, not a credential

var (
	sampleAPIKeyName         = "Shop quoting agent"
	sampleAPIKeyClientSource = "docs"
	sampleAPIKeyLastUsedAt   = SampleCreatedAt.Add(2 * time.Hour)
)

var SampleAPIKey = &APIKey{
	ID:            SampleAPIKeyID,
	Object:        constants.ObjectTypeAPIKey,
	Name:          &sampleAPIKeyName,
	Client:        &APIKeyClient{Name: "claude-code", Source: &sampleAPIKeyClientSource},
	RedactedValue: "dfm_sk_****d7f9",
	Status:        constants.APIKeyStatusActive,
	LastUsedAt:    &sampleAPIKeyLastUsedAt,
	CreatedAt:     SampleCreatedAt,
	UpdatedAt:     SampleCreatedAt,
}

func (*APIKey) SchemaExample() any {
	return example.ValidateAndMarshalToMap(SampleAPIKey)
}

var SampleCreatedAPIKey = &CreatedAPIKey{
	Object: constants.ObjectTypeCreatedAPIKey,
	Secret: "dfm_sk_4k2m9x1pq7w3e8r5t2y6u0i4o1a3s5d7f9", // #nosec G101 - a documentation sample, not a credential
	APIKey: *SampleAPIKey,
}

func (*CreatedAPIKey) SchemaExample() any {
	return example.ValidateAndMarshalToMap(SampleCreatedAPIKey)
}

// SampleCreatedAt is the instant every sample resource was created at.
var SampleCreatedAt = time.Date(2026, 10, 10, 14, 30, 0, 0, time.UTC)
