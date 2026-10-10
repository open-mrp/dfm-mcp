// Package app registers what the kit needs to know for the engine's errors to carry the API's codes. Every binary and test calls Register once.
package app

import (
	"sync"

	"github.com/open-mrp/apikit/apierror"

	"github.com/open-mrp/dfm-mcp/internal/apierrors"
)

var once sync.Once

// Register wires the error codes into the kit. It is safe to call more than once.
func Register() {
	once.Do(func() {
		apierrors.Register()
		apierror.SetDocURL(func(c apierror.Code) string { return "https://docs.openmrp.ai/dfm/errors/" + string(c) })
	})
}
