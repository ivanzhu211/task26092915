package funnel

import (
	"errors"
	"fmt"
	"time"
)

type Channel string

const (
	ChannelEmail      Channel = "Email"
	ChannelWebsite    Channel = "Website"
	ChannelInStore    Channel = "In-Store"
	ChannelMobileApp  Channel = "Mobile App"
	ChannelCallCenter Channel = "Call Center"
	ChannelSocial     Channel = "Social Media"
)

var validChannels = map[Channel]bool{
	ChannelEmail:      true,
	ChannelWebsite:    true,
	ChannelInStore:    true,
	ChannelMobileApp:  true,
	ChannelCallCenter: true,
	ChannelSocial:     true,
}

type EventType string

const (
	EventPageView    EventType = "page_view"
	EventEmailOpen   EventType = "email_open"
	EventEmailClick  EventType = "email_click"
	EventAddToCart   EventType = "add_to_cart"
	EventCartAbandon EventType = "cart_abandon"
	EventPurchase    EventType = "purchase"
	EventSupportCall EventType = "support_call"
)

var validEventTypes = map[EventType]bool{
	EventPageView:    true,
	EventEmailOpen:   true,
	EventEmailClick:  true,
	EventAddToCart:   true,
	EventCartAbandon: true,
	EventPurchase:    true,
	EventSupportCall: true,
}

type Touchpoint struct {
	CustomerID string
	OccurredAt time.Time
	Channel    Channel
	Event      EventType
}

const MaxBatchSize = 5000

type BatchError struct {
	Index int
	Err   error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("touchpoint at index %d invalid: %v", e.Index, e.Err)
}

func (e *BatchError) Unwrap() error { return e.Err }

var (
	ErrBatchTooLarge   = errors.New("batch exceeds 5000 touchpoints")
	ErrEmptyCustomerID = errors.New("customer id is empty")
	ErrInvalidChannel  = errors.New("unknown channel")
	ErrInvalidEvent    = errors.New("unknown event type")
)

func validateTouchpoint(tp Touchpoint) error {
	if tp.CustomerID == "" {
		return ErrEmptyCustomerID
	}
	if !validChannels[tp.Channel] {
		return ErrInvalidChannel
	}
	if !validEventTypes[tp.Event] {
		return ErrInvalidEvent
	}
	return nil
}

type Step struct {
	Event    EventType
	Channels []Channel
}

func (s Step) matches(tp Touchpoint) bool {
	if tp.Event != s.Event {
		return false
	}
	if len(s.Channels) == 0 {
		return true
	}
	for _, c := range s.Channels {
		if tp.Channel == c {
			return true
		}
	}
	return false
}

type Params struct {
	Steps            []Step
	WindowStart      time.Time
	WindowEnd        time.Time
	ConversionWindow time.Duration
}

const (
	MinSteps = 2
	MaxSteps = 10

	MinConversionWindow = time.Minute
	MaxConversionWindow = 90 * 24 * time.Hour

	MaxAnalysisPeriod = 365 * 24 * time.Hour
)

var (
	ErrStepCount       = errors.New("funnel must have between 2 and 10 steps")
	ErrStepEvent       = errors.New("step has unknown event type")
	ErrStepChannel     = errors.New("step has unknown channel")
	ErrPeriodRange     = errors.New("analysis period must be a positive half-open range of at most 365 days")
	ErrConversionRange = errors.New("conversion window must be between 1 minute and 90 days")
)

func (p Params) validate() error {
	if len(p.Steps) < MinSteps || len(p.Steps) > MaxSteps {
		return ErrStepCount
	}
	for _, s := range p.Steps {
		if !validEventTypes[s.Event] {
			return ErrStepEvent
		}
		for _, c := range s.Channels {
			if !validChannels[c] {
				return ErrStepChannel
			}
		}
	}
	if !p.WindowEnd.After(p.WindowStart) || p.WindowEnd.Sub(p.WindowStart) > MaxAnalysisPeriod {
		return ErrPeriodRange
	}
	if p.ConversionWindow < MinConversionWindow || p.ConversionWindow > MaxConversionWindow {
		return ErrConversionRange
	}
	return nil
}

type StepResult struct {
	Reached          int
	RateFromPrevious *float64
	RateFromFirst    *float64
	MedianGapSeconds *int64
	Bottleneck       bool
}

type Result struct {
	Steps []StepResult
}
