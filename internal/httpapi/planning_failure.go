package httpapi

import (
	"context"
	"errors"

	"github.com/Chibuoyimm/qa-agent/internal/chatgpt"
	"github.com/Chibuoyimm/qa-agent/internal/opencode"
	"github.com/Chibuoyimm/qa-agent/internal/planner"
	"github.com/Chibuoyimm/qa-agent/internal/qa"
)

// Only these application-owned messages are saved. Upstream bodies and model output stay private.
func planningFailure(err error) (qa.PlanningFailure, string) {
	failure := qa.PlanningFailure{Code: "internal_error", Retryable: true}
	message := "Planning could not finish because of an application error. Review your saved request and try again."
	switch {
	case errors.Is(err, context.Canceled):
		failure.Code = "interrupted"
		message = "Planning was interrupted. Review your saved request and try again."
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, planner.ErrTimeout):
		failure.Code = "timeout"
		message = "The AI provider took too long to prepare the plan. Try again, or split the request into a smaller journey."
	case errors.Is(err, planner.ErrInvalidPlan):
		failure.Code = "invalid_plan"
		// Plan validation errors contain rule names and step numbers, never field values.
		failure.Detail = err.Error()
		message = "The AI returned a plan with invalid or unsupported steps. Review the request and try drafting again."
	case errors.Is(err, planner.ErrIncompleteResponse):
		failure.Code = "incomplete_response"
		message = "The AI response ended before a complete plan arrived. The partial response was not saved as a plan. Try drafting again."
	case errors.Is(err, planner.ErrInvalidResponse):
		failure.Code = "invalid_response"
		message = "The AI response did not match the required format. Try drafting again."
	case errors.Is(err, planner.ErrResponseLimit):
		failure.Code = "response_limit"
		message = "The AI response exceeded the planning limit. Split the request into a smaller journey before trying again."
	case errors.Is(err, planner.ErrProviderQuota), errors.Is(err, chatgpt.ErrUsageLimit), errors.Is(err, opencode.ErrQuota):
		failure = qa.PlanningFailure{Code: "usage_limit"}
		message = "The selected AI connection reached a usage or rate limit. Check its usage or choose another available connection before trying again."
	case errors.Is(err, chatgpt.ErrReconnect), errors.Is(err, planner.ErrProviderAuth), errors.Is(err, planner.ErrSubscriptionDenied), errors.Is(err, chatgpt.ErrInvalid), errors.Is(err, opencode.ErrAuth), errors.Is(err, opencode.ErrInvalid):
		failure = qa.PlanningFailure{Code: "connection_required"}
		message = "The selected AI account, key or model is unavailable. Review the AI connection before trying again."
	case errors.Is(err, planner.ErrProviderRequest), errors.Is(err, planner.ErrSubscriptionCapability), errors.Is(err, opencode.ErrRequest):
		failure = qa.PlanningFailure{Code: "unsupported_configuration"}
		message = "The provider rejected this model or planning configuration. Review the AI connection and model before trying again."
	case errors.Is(err, opencode.ErrCleanup):
		failure = qa.PlanningFailure{Code: "connection_required"}
		message = "The AI connection needs attention after its previous request. Review the connection before trying again."
	case errors.Is(err, planner.ErrProviderUnavailable), errors.Is(err, planner.ErrSubscriptionUnavailable), errors.Is(err, chatgpt.ErrUpstream), errors.Is(err, opencode.ErrUnavailable):
		failure.Code = "provider_unavailable"
		message = "The selected AI connection is temporarily unavailable. Try again later."
	case errors.Is(err, planner.ErrInvalid):
		failure.Code = "request_invalid"
		message = "The request and its context could not be used for planning. Review the context size and selected sources before trying again."
	case errors.Is(err, planner.ErrBusy), errors.Is(err, opencode.ErrBusy):
		failure.Code = "busy"
		message = "Another planning request is still using this AI connection. Wait for it to finish before trying again."
	case errors.Is(err, planner.ErrUpstream), errors.Is(err, opencode.ErrUpstream):
		failure.Code = "provider_failed"
		message = "The AI provider could not finish planning. Review your saved request and try again."
	}
	return failure, message + " No checks were run."
}
