// Package compact 提供 eino 框架通用的上下文压缩插件.
// 实现基于 Claude Code 的 4 层递进压缩体系:
//   Layer 1: MicroCompact  - 规则清理旧工具输出 (<1ms)
//   Layer 2: AutoCompact   - 阈值触发 + 断路器
//   Layer 3: FullCompact   - LLM 摘要生成 (5-30s)
//   Layer 4: SessionMemory - 复用已有摘要 (<10ms)
//
// 集成方式见 examples/eino_demo/ 目录下的完整示例.
package compact
