package compact

// CompactionResult 压缩结果.
type CompactionResult struct {
	WasCompacted   bool      `json:"was_compacted"`
	Messages       []Message `json:"messages"`
	TokensBefore   int       `json:"tokens_before"`
	TokensAfter    int       `json:"tokens_after"`
	Trigger        string    `json:"trigger"`
	Summary        string    `json:"summary,omitempty"`
	BoundaryMarker bool      `json:"boundary_marker,omitempty"`
}

// CompactionInfo 压缩事件信息.
type CompactionInfo struct {
	Trigger      string `json:"trigger"`
	TokensBefore int    `json:"tokens_before"`
	Threshold    int    `json:"threshold"`
	Layer        int    `json:"layer"`
}
