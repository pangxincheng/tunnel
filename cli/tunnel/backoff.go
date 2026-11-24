package tunnel

import (
	"math/rand"
	"time"
)

// Backoff 实现带抖动的指数退避,用于断线后的重连节奏控制。
// 抖动(±20%)避免服务端恢复瞬间所有客户端在同一时刻重连形成惊群。
type Backoff struct {
	min, max time.Duration
	cur      time.Duration
	rng      *rand.Rand
}

func NewBackoff(min, max time.Duration) *Backoff {
	return &Backoff{min: min, max: max, cur: min, rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

// Next 返回本次应等待的时长(基准值 ±20% 抖动),随后基准值翻倍,封顶 max。
func (b *Backoff) Next() time.Duration {
	base := b.cur
	b.cur *= 2
	if b.cur > b.max {
		b.cur = b.max
	}
	return time.Duration(float64(base) * (0.8 + 0.4*b.rng.Float64()))
}

// Reset 在连接曾稳定存活后调用,把基准值降回 min。
func (b *Backoff) Reset() { b.cur = b.min }
