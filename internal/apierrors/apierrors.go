// Package apierrors registers this API's error codes with the kit and converts the engine's errors into the kit's.
package apierrors

import (
	"net/http"

	"github.com/open-mrp/apikit/apierror"
	dfmapierror "github.com/open-mrp/dfm/apierror"
	dfmconstants "github.com/open-mrp/dfm/constants"
)

// Codes the engine reports about the design file itself. They are the engine's codes, re-registered here so the kit knows their status and documents them.
const (
	CodeDesignInvalid            apierror.Code = apierror.Code(dfmconstants.ErrorCodeDesignInvalid)
	CodeDesignFormatUnsupported  apierror.Code = apierror.Code(dfmconstants.ErrorCodeDesignFormatUnsupported)
	CodeDesignContentUnsupported apierror.Code = apierror.Code(dfmconstants.ErrorCodeDesignContentUnsupported)
	CodeDesignUnitsUnknown       apierror.Code = apierror.Code(dfmconstants.ErrorCodeDesignUnitsUnknown)
	// CodeDesignRetentionUnavailable is returned when a caller asks for the design to be retained on a deployment with no design store.
	CodeDesignRetentionUnavailable apierror.Code = "design_retention_unavailable"
	// CodeDesignMismatch is returned when a design sent with a feedback report is not the one the check ran on.
	CodeDesignMismatch apierror.Code = "design_mismatch"
	// CodeDesignNotUploaded says a check was asked for on a design whose file has not arrived.
	CodeDesignNotUploaded apierror.Code = "design_not_uploaded"
	// CodeDesignDiscarded says a check was asked for on a design whose file is gone: a check already ran on it, or it expired unchecked.
	CodeDesignDiscarded apierror.Code = "design_discarded"
)

// Register adds this API's codes to the kit's registry. Call it once at startup, before serving or generating the spec.
func Register() {
	for _, s := range []apierror.Spec{
		{Code: CodeDesignInvalid, Type: apierror.TypeInvalidRequest, Status: http.StatusUnprocessableEntity,
			Description: "The design file could not be read: it is malformed, truncated or not the format it claims. Fix the file and resend it."},
		{Code: CodeDesignFormatUnsupported, Type: apierror.TypeInvalidRequest, Status: http.StatusUnprocessableEntity,
			Description: "The design format is not one the engine reads. Export the design as DXF, SVG or STEP."},
		{Code: CodeDesignContentUnsupported, Type: apierror.TypeInvalidRequest, Status: http.StatusUnprocessableEntity,
			Description: "The design file holds content the engine cannot check, such as 3D geometry that is not sheet metal, an external reference or a file over the size limit. The message says which."},
		{Code: CodeDesignUnitsUnknown, Type: apierror.TypeInvalidRequest, Status: http.StatusUnprocessableEntity,
			Description: "The design file declares no drawing units. Send `design.units` to say how to read it."},
		{Code: CodeDesignRetentionUnavailable, Type: apierror.TypeInvalidRequest, Status: http.StatusUnprocessableEntity,
			Description: "This deployment cannot retain design files. Send `design.retention` as `none`, or omit it."},
		{Code: CodeDesignMismatch, Type: apierror.TypeInvalidRequest, Status: http.StatusUnprocessableEntity,
			Description: "The design sent with the report is not the file the check ran on: its content differs. Send the same file the check was created with."},
		{Code: CodeDesignNotUploaded, Type: apierror.TypeInvalidRequest, Status: http.StatusConflict,
			Description: "The design's file has not been uploaded. Send it to the design's `upload.url` first, then ask for the check again."},
		{Code: CodeDesignDiscarded, Type: apierror.TypeInvalidRequest, Status: http.StatusGone,
			Description: "The design's file is gone: a check already ran on it, or it expired before one did. Create a new design and upload the file again."},
	} {
		apierror.Register(s)
	}
}

// FromEngine converts an engine error into the kit's error with the matching status and field list. Parameter errors become a 422 that lists every failing parameter, so a client fixes them all in one round trip; design errors keep the engine's code; anything else is a 500.
func FromEngine(e *dfmapierror.Error) *apierror.APIError {
	if e == nil {
		return nil
	}
	switch e.Code {
	case dfmconstants.ErrorCodeDesignInvalid, dfmconstants.ErrorCodeDesignFormatUnsupported,
		dfmconstants.ErrorCodeDesignContentUnsupported, dfmconstants.ErrorCodeDesignUnitsUnknown:
		return apierror.New(apierror.Code(e.Code), e.Message, apierror.WithParam(e.Param), apierror.WithInternal(e.Internal))
	case dfmconstants.ErrorCodeInvalidFormat, dfmconstants.ErrorCodeParameterMissing,
		dfmconstants.ErrorCodeParameterUnknown, dfmconstants.ErrorCodeParameterInvalid:
		fields := make([]apierror.FieldError, 0, len(e.Errors)+1)
		if len(e.Errors) == 0 {
			fields = append(fields, apierror.Field(e.Param, fieldCode(e.Code), e.Message))
		}
		for _, fe := range e.Errors {
			fields = append(fields, apierror.Field(fe.Param, fieldCode(fe.Code), fe.Message))
		}
		return apierror.NewValidationError(e.Message, fields...)
	default:
		return apierror.NewInternalError(e.Internal, e.Message)
	}
}

// fieldCode maps an engine parameter code onto the codes a 422's field list carries.
func fieldCode(c dfmconstants.ErrorCode) apierror.Code {
	switch c {
	case dfmconstants.ErrorCodeParameterMissing:
		return apierror.CodeMissingField
	case dfmconstants.ErrorCodeParameterUnknown:
		return apierror.CodeParameterUnknown
	default:
		return apierror.CodeInvalidFormat
	}
}
