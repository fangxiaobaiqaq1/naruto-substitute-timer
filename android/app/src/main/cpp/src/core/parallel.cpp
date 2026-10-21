// nt/parallel.h 实现（architect 所有）：常驻小线程池，调用线程参与执行。
//
// 嵌套：池线程里发起的 ParallelFor 也把任务放进队列，空闲的池线程可以帮忙；发起者
// 自己领取下标直到领完，然后只等待别人手里正在执行的下标——等待中的线程从不执行
// 其它任务，正在执行的线程也从不等待一个被等待线程持有的下标，因此不会死锁。
#include "nt/parallel.h"

#include <algorithm>
#include <atomic>
#include <condition_variable>
#include <deque>
#include <exception>
#include <memory>
#include <mutex>
#include <thread>
#include <vector>

namespace nt {

namespace {

std::atomic<bool> g_enabled{true};

struct Job {
    int n = 0;
    IndexFn fn;
    std::atomic<int> next{0};
    std::atomic<int> done{0};
    std::mutex mu;
    std::condition_variable cv;
    std::exception_ptr error;

    // 领取并执行下标，直到领完。
    void run() {
        for (;;) {
            int i = next.fetch_add(1, std::memory_order_relaxed);
            if (i >= n) return;
            try {
                fn(i);
            } catch (...) {
                std::lock_guard<std::mutex> lock(mu);
                if (!error) error = std::current_exception();
            }
            if (done.fetch_add(1, std::memory_order_acq_rel) + 1 == n) {
                std::lock_guard<std::mutex> lock(mu);
                cv.notify_all();
            }
        }
    }
};

class Pool {
public:
    static Pool& Instance() {
        static Pool* pool = new Pool();  // 故意泄漏：进程退出时不 join（避免静态析构顺序问题）
        return *pool;
    }

    int workers() const { return workers_; }

    // All or nothing: deque::insert at the end has no effect when it throws
    // (bad_alloc for a node), so a failed submit leaves no queued entry behind.
    void submit(const std::shared_ptr<Job>& job, int helpers) {
        {
            std::lock_guard<std::mutex> lock(mu_);
            queue_.insert(queue_.end(), static_cast<size_t>(helpers), job);
        }
        if (helpers == 1) cv_.notify_one();
        else cv_.notify_all();
    }

private:
    // Each worker is detached as soon as it runs. A thread that cannot be
    // created (pthread_create EAGAIN: thread or memory limits) leaves the pool
    // smaller, possibly empty (then everything runs on the caller), instead of
    // destroying a joinable std::thread, which is std::terminate.
    Pool() {
        unsigned hc = std::thread::hardware_concurrency();
        int n = hc > 1 ? static_cast<int>(hc) - 1 : 0;
        n = std::min(n, kMaxWorkers);
        for (int i = 0; i < n; ++i) {
            try {
                std::thread([this] { loop(); }).detach();
            } catch (...) {
                break;
            }
            ++workers_;
        }
    }

    void loop() {
        for (;;) {
            std::shared_ptr<Job> job;
            {
                std::unique_lock<std::mutex> lock(mu_);
                cv_.wait(lock, [this] { return !queue_.empty(); });
                job = std::move(queue_.front());
                queue_.pop_front();
            }
            // 已被领完的任务（发起者自己做完了）直接跳过，不占用线程。
            if (job->next.load(std::memory_order_relaxed) < job->n) job->run();
        }
    }

    std::mutex mu_;
    std::condition_variable cv_;
    std::deque<std::shared_ptr<Job>> queue_;
    int workers_ = 0;  // fixed once Instance() returns
};

}  // namespace

void SetParallelEnabled(bool on) { g_enabled.store(on, std::memory_order_relaxed); }
bool ParallelEnabled() { return g_enabled.load(std::memory_order_relaxed); }

void ParallelForRef(int n, IndexFn fn) {
    if (n <= 0) return;
    if (n == 1 || !ParallelEnabled()) {
        for (int i = 0; i < n; ++i) fn(i);
        return;
    }
    Pool& pool = Pool::Instance();
    int helpers = std::min(pool.workers(), n - 1);
    if (helpers <= 0) {
        for (int i = 0; i < n; ++i) fn(i);
        return;
    }
    auto job = std::make_shared<Job>();
    job->n = n;
    job->fn = fn;
    try {
        pool.submit(job, helpers);
    } catch (...) {
        // Out of memory while queueing: no helper has the job. fn refers to the
        // caller's frame, so nothing may escape before every index is done;
        // the caller simply runs them all (the wait below returns at once).
    }
    job->run();  // 调用线程也是 worker
    {
        std::unique_lock<std::mutex> lock(job->mu);
        job->cv.wait(lock, [&] { return job->done.load(std::memory_order_acquire) == n; });
    }
    if (job->error) std::rethrow_exception(job->error);
}

}  // namespace nt
