package compact

import (
	"container/list"
	"hash/fnv"
	"sync"
)

// TokenCache 是一个并发安全的 LRU，用于缓存"内容串 → token 数"映射.
//
// 设计目标:
//   - 默认 *不启用*：调用方按需创建并通过 EstimateMessageTokensWithCache 显式传入
//   - 只缓存大块内容（短串本来就 O(len/4)，缓存反而更慢）
//   - 用 FNV-1a 而非加密哈希，键空间冲突可以接受 —— 估算误差最多偏 1 个 block
//
// 适用场景:
//   - Agent 主循环里同一批消息会被 ShouldCompact + Compact 反复扫描
//   - 单元测试 / benchmark 场景反复构造相似消息
//
// 不适用场景:
//   - 消息内容每次都变化（命中率低，反而是负优化）
type TokenCache struct {
	mu          sync.Mutex
	capacity    int
	items       map[uint64]*list.Element
	order       *list.List
	minBlockLen int
}

type cacheEntry struct {
	key    uint64
	tokens int
}

// NewTokenCache 创建容量为 capacity 的 LRU。
//
//	capacity <= 0 时强制使用 1024 —— 太小的缓存价值不大，太大又容易吃内存。
//	minBlockLen 控制只缓存 len(content) >= 阈值的块（默认 512 字符）。
func NewTokenCache(capacity int) *TokenCache {
	if capacity <= 0 {
		capacity = 1024
	}
	return &TokenCache{
		capacity:    capacity,
		items:       make(map[uint64]*list.Element, capacity),
		order:       list.New(),
		minBlockLen: 512,
	}
}

// WithMinBlockLen 调整最小缓存阈值；返回自身以便链式调用.
func (c *TokenCache) WithMinBlockLen(n int) *TokenCache {
	c.minBlockLen = n
	return c
}

// hash 用 FNV-1a 算 content 的 64bit 哈希.
func (c *TokenCache) hash(content string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(content))
	return h.Sum64()
}

// EstimateBlock 估算单个内容串 token 数，命中缓存时直接返回。
//
//	content 较短（< minBlockLen）时跳过缓存 —— hash 比 len/4 更慢。
func (c *TokenCache) EstimateBlock(content string) int {
	if len(content) < c.minBlockLen {
		return RoughTokenEstimate(content)
	}
	key := c.hash(content)

	c.mu.Lock()
	if elem, ok := c.items[key]; ok {
		c.order.MoveToFront(elem)
		tokens := elem.Value.(*cacheEntry).tokens
		c.mu.Unlock()
		return tokens
	}
	c.mu.Unlock()

	tokens := RoughTokenEstimate(content)

	c.mu.Lock()
	defer c.mu.Unlock()
	// 双重检查：可能并发已经写入
	if elem, ok := c.items[key]; ok {
		c.order.MoveToFront(elem)
		return elem.Value.(*cacheEntry).tokens
	}
	elem := c.order.PushFront(&cacheEntry{key: key, tokens: tokens})
	c.items[key] = elem
	for len(c.items) > c.capacity {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*cacheEntry).key)
	}
	return tokens
}

// Size 返回当前缓存条目数，仅用于测试与可观测性.
func (c *TokenCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// Clear 清空缓存.
func (c *TokenCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[uint64]*list.Element, c.capacity)
	c.order = list.New()
}

// EstimateMessageTokensWithCache 与 EstimateMessageTokens 行为一致，
// 但对长文本块使用 cache 加速.
//
// cache 为 nil 时直接退化为 EstimateMessageTokens（零开销）。
func EstimateMessageTokensWithCache(messages []Message, cache *TokenCache) int {
	if cache == nil {
		return EstimateMessageTokens(messages)
	}
	total := 0
	for _, msg := range messages {
		for _, block := range msg.Content {
			total += estimateBlockTokensWithCache(block, cache)
		}
	}
	// 与原实现保持一致的保守填充
	return int(float64(total)*conservativeMultiplier + 0.999)
}

func estimateBlockTokensWithCache(block ContentBlock, cache *TokenCache) int {
	switch block.Type {
	case ContentTypeText:
		return cache.EstimateBlock(block.Text)
	case ContentTypeToolResult:
		return cache.EstimateBlock(block.ToolOutput)
	case ContentTypeImage:
		return imageDocumentTokenEstimate
	case ContentTypeThinking:
		return cache.EstimateBlock(block.Thinking)
	case ContentTypeToolUse:
		// tool_use 通常较短，不缓存
		return estimateBlockTokens(block)
	default:
		return estimateBlockTokens(block)
	}
}
