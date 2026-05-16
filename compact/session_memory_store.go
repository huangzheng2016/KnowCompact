package compact

import (
	"context"
	"strings"
	"sync"
)

// InMemorySessionMemoryStore 是 SessionMemoryStore 的开箱即用内存实现.
//
// 适用场景:
//   - 单进程 / 单会话的开发与示例代码
//   - 评测脚本、测试用例
//   - 接入真正的持久层（Redis/SQL/向量库）之前的快速原型
//
// 不适用场景:
//   - 多副本 / 多用户的生产环境（应自行实现持久化 + 隔离的 Store）
//
// 线程安全：所有访问通过 RWMutex 保护，读多写少的并发负载下性能良好.
type InMemorySessionMemoryStore struct {
	mu                       sync.RWMutex
	memory                   string
	lastSummarizedMessageID  string
}

// NewInMemorySessionMemoryStore 创建空的内存 Session Memory.
func NewInMemorySessionMemoryStore() *InMemorySessionMemoryStore {
	return &InMemorySessionMemoryStore{}
}

// GetMemory 返回当前累积的 Session Memory 文本.
func (s *InMemorySessionMemoryStore) GetMemory(_ context.Context) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.memory, nil
}

// GetLastSummarizedMessageID 返回上次被纳入摘要的消息 ID.
func (s *InMemorySessionMemoryStore) GetLastSummarizedMessageID(_ context.Context) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSummarizedMessageID
}

// IsEmpty 当 memory 为空白时返回 true.
func (s *InMemorySessionMemoryStore) IsEmpty(_ context.Context) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.memory) == ""
}

// SetMemory 覆盖当前 memory（典型用法：后台记忆提取协程定期写入）.
func (s *InMemorySessionMemoryStore) SetMemory(memory string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memory = memory
}

// AppendMemory 追加一段 memory 内容，原子操作.
//
// 用于"增量更新"模式：每轮对话后追加一段新摘要片段，避免覆盖之前的累积.
// 自动在原 memory 与新内容之间插入空行分隔.
func (s *InMemorySessionMemoryStore) AppendMemory(snippet string) {
	if strings.TrimSpace(snippet) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.memory == "" {
		s.memory = snippet
		return
	}
	s.memory = s.memory + "\n\n" + snippet
}

// SetLastSummarizedMessageID 记录最近一次被纳入摘要的消息 ID.
//
// SessionMemoryCompactor 据此判断哪些消息已被摘要、哪些应原样保留.
func (s *InMemorySessionMemoryStore) SetLastSummarizedMessageID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSummarizedMessageID = id
}

// Clear 重置 memory 与 lastSummarizedMessageID.
func (s *InMemorySessionMemoryStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memory = ""
	s.lastSummarizedMessageID = ""
}

// 编译期接口检查：确保实现了 SessionMemoryStore.
var _ SessionMemoryStore = (*InMemorySessionMemoryStore)(nil)
