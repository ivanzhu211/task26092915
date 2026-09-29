// Package funnel 提供客户旅程漏斗分析能力：触点批量写入本地 SQLite，
// 并按漏斗规则做转化分析。本包为库，由上层分析服务调用，不含接口层。
package funnel

import (
	"fmt"
	"time"
)

// Channel 触点渠道，仅允许以下六种。
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

// EventType 触点事件类型，仅允许以下七种。
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

// Touchpoint 一条客户触点。OccurredAt 精确到秒。
type Touchpoint struct {
	CustomerID string
	OccurredAt time.Time
	Channel    Channel
	EventType  EventType
}

// BatchError 批量写入校验失败。Index 为批内第一条不合法记录的下标（从 0 开始）。
type BatchError struct {
	Index  int
	Reason string
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("funnel: invalid touchpoint at batch index %d: %s", e.Index, e.Reason)
}

// MaxBatchSize 每批最多写入的触点条数。
const MaxBatchSize = 5000

// FunnelStep 漏斗的一步：指定事件类型，可再限定为若干渠道之一（空表示不限渠道）。
type FunnelStep struct {
	EventType EventType
	Channels  []Channel
}

// matches 判断触点是否满足该步条件。
func (s FunnelStep) matches(t Touchpoint) bool {
	if t.EventType != s.EventType {
		return false
	}
	if len(s.Channels) == 0 {
		return true
	}
	for _, c := range s.Channels {
		if t.Channel == c {
			return true
		}
	}
	return false
}

// 分析参数限制。
const (
	MinSteps     = 2
	MaxSteps     = 10
	MaxRangeDays = 365
	MinWindow    = time.Minute
	MaxWindow    = 90 * 24 * time.Hour
)

// AnalysisRequest 一次漏斗分析的参数。
// 时间段为左闭右开 [Start, End)，最长 365 天；Window 为转化窗口（1 分钟到 90 天）。
type AnalysisRequest struct {
	Steps  []FunnelStep
	Start  time.Time
	End    time.Time
	Window time.Duration
}

// Validate 校验分析参数。
func (r AnalysisRequest) Validate() error {
	if len(r.Steps) < MinSteps || len(r.Steps) > MaxSteps {
		return fmt.Errorf("funnel: steps must be between %d and %d, got %d", MinSteps, MaxSteps, len(r.Steps))
	}
	for i, s := range r.Steps {
		if !validEventTypes[s.EventType] {
			return fmt.Errorf("funnel: step %d has invalid event type %q", i, s.EventType)
		}
		for _, c := range s.Channels {
			if !validChannels[c] {
				return fmt.Errorf("funnel: step %d has invalid channel %q", i, c)
			}
		}
	}
	if !r.End.After(r.Start) {
		return fmt.Errorf("funnel: end must be after start")
	}
	if r.End.Sub(r.Start) > MaxRangeDays*24*time.Hour {
		return fmt.Errorf("funnel: time range must not exceed %d days", MaxRangeDays)
	}
	if r.Window < MinWindow || r.Window > MaxWindow {
		return fmt.Errorf("funnel: window must be between %s and %s", MinWindow, MaxWindow)
	}
	return nil
}

// StepStats 漏斗某一步的统计结果。
type StepStats struct {
	// Reached 到达该步的客户数。
	Reached int
	// ConvFromPrev 相对上一步的转化率（四位小数，四舍五入）；第 1 步或分母为 0 时为 nil。
	ConvFromPrev *float64
	// ConvFromFirst 相对第 1 步的转化率（四位小数，四舍五入）；第 1 步或分母为 0 时为 nil。
	ConvFromFirst *float64
	// MedianGapSeconds 从上一步到该步间隔的中位数（秒，偶数个取中间两个平均后向下取整）；
	// 第 1 步或没人到达时为 nil。
	MedianGapSeconds *int64
	// Bottleneck 相对上一步流失严格超过 40% 时标为卡点（恰好 40% 不算；上一步没人时不标）。
	Bottleneck bool
}

// AnalysisResult 一次漏斗分析的结果，Steps 与请求的 Steps 一一对应。
type AnalysisResult struct {
	Steps []StepStats
}
