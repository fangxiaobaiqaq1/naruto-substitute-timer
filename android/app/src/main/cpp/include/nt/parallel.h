// nt/parallel.h — 识别核心共用的有界并行（architect 所有，实现在 src/core/parallel.cpp）。
//
// 替代 Go 里的 goroutine + sync.WaitGroup / scene.forEach / ninja.parallelFor /
// rgb.parallelSides。规则（所有 porter 必读）：
//  * 结果必须与串行完全一致：并行体只写自己的下标槽位，归约一律在调用方按原始
//    下标顺序、用与 Go 相同的比较（严格 '>' 等）完成。不得依赖执行顺序。
//  * 线程池常驻（进程级，最多 kMaxWorkers 个后台线程 + 调用线程本身），
//    不会每帧创建线程。调用线程总是参与执行自己的任务、领完所有下标后才等待，
//    等待时不执行别的任务，因此嵌套调用不会死锁。池线程内发起的嵌套调用同样可以
//    让空闲的池线程帮忙（例如左右两侧各自的头像打分），线程总数仍不超过上限。
//  * 并行体内禁止抛异常越过 ParallelFor（异常会被捕获并在调用线程重新抛出第一个）。
//  * 共享可变状态（Reader 的缩放缓存、AvatarCatalog 的模板缓存）由其所有者按 Go
//    的锁粒度加 std::mutex 保护；Tracker / AvatarTracker 只被一个并行体使用。
#pragma once

#include <cstddef>
#include <functional>
#include <type_traits>
#include <utility>

namespace nt {

constexpr int kMaxWorkers = 3;  // 后台线程上限（模拟器通常 4 核）

// 进程级开关：false 时一切串行（等价 Go 测试里的 parallelSides=false）。
void SetParallelEnabled(bool on);
bool ParallelEnabled();

// 类型擦除的 fn(i) 引用（不分配内存；只在 ParallelFor 调用期间有效）。
struct IndexFn {
    void* ctx = nullptr;
    void (*call)(void* ctx, int i) = nullptr;
    void operator()(int i) const { call(ctx, i); }
};

// fn(i) for i in [0, n)。n <= 1 或开关关闭时串行内联执行。
void ParallelForRef(int n, IndexFn fn);

template <class F>
void ParallelFor(int n, F&& fn) {
    using Fn = std::remove_reference_t<F>;
    IndexFn ref;
    ref.ctx = const_cast<void*>(static_cast<const void*>(&fn));
    ref.call = [](void* ctx, int i) { (*static_cast<Fn*>(ctx))(i); };
    ParallelForRef(n, ref);
}

// 并行执行两段（Go: wg.Go(b); a(); wg.Wait()）。a、b 可能在任一线程执行，返回时两者都已完成。
template <class A, class B>
void ParallelInvoke(A&& a, B&& b) {
    ParallelFor(2, [&](int i) {
        if (i == 0) a();
        else b();
    });
}

}  // namespace nt
