package ac

import (
	"fmt"
	"testing"
)

// BenchmarkUpdate 基准测试 Update 操作性能
func BenchmarkUpdate(b *testing.B) {
	m := New(WithNodeID("bench-node"))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			m.Update(fmt.Sprintf("key_%d", i%100), 1.0)
			i++
		}
	})
}

// BenchmarkGet 基准测试 Get 操作性能
func BenchmarkGet(b *testing.B) {
	m := New(WithNodeID("bench-node"))

	// 预填充数据
	for i := 0; i < 1000; i++ {
		m.Update(fmt.Sprintf("key_%d", i), float64(i))
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_ = m.Get(fmt.Sprintf("key_%d", i%1000))
			i++
		}
	})
}

// BenchmarkSnapshot 基准测试 Snapshot 性能
func BenchmarkSnapshot(b *testing.B) {
	m := New(WithNodeID("bench-node"))

	// 预填充数据
	for i := 0; i < 1000; i++ {
		m.Update(fmt.Sprintf("key_%d", i), float64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Snapshot()
	}
}

// BenchmarkConcurrentUpdates 基准测试并发更新性能
func BenchmarkConcurrentUpdates(b *testing.B) {
	m := New(WithNodeID("bench-node"))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			m.Update(fmt.Sprintf("concurrent_key_%d", i%50), 1.0)
			i++
		}
	})
}

// BenchmarkMultipleKeys 多键操作基准测试
func BenchmarkMultipleKeys(b *testing.B) {
	m := New(WithNodeID("bench-node"))
	keys := []string{"link_1_bw", "link_2_bw", "link_3_bw", "link_4_bw", "link_5_bw"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j, key := range keys {
			m.Update(key, float64(j+1))
		}
	}
}
