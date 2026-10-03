// nt/parallel.h 实现（architect 所有）：常驻小线程池，调用线程参与执行。
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
thread_local bool t_inPool = false;

struct Job {
    int n = 0;
    const std::function<void(int)>* fn = nullptr;
    std::atomic<int> next{0};
    std::atomic<int> done{0};
    std::mutex mu;
    std::condition_variable cv;
    std::exception_ptr error;

    // 领取并执行下标，直到领完。返回本线程是否执行了最后一个。
    void run() {
        for (;;) {
            int i = next.fetch_add(1, std::memory_order_relaxed);
            if (i >= n) return;
            try {
                (*fn)(i);
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

    int workers() const { return static_cast<int>(threads_.size()); }

    void submit(const std::shared_ptr<Job>& job, int helpers) {
        {
            std::lock_guard<std::mutex> lock(mu_);
            for (int k = 0; k < helpers; ++k) queue_.push_back(job);
        }
        if (helpers == 1) cv_.notify_one();
        else cv_.notify_all();
    }

private:
    Pool() {
        unsigned hc = std::thread::hardware_concurrency();
        int n = hc > 1 ? static_cast<int>(hc) - 1 : 0;
        n = std::min(n, kMaxWorkers);
        for (int i = 0; i < n; ++i) threads_.emplace_back([this] { loop(); });
        for (auto& t : threads_) t.detach();
    }

    void loop() {
        t_inPool = true;
        for (;;) {
            std::shared_ptr<Job> job;
            {
                std::unique_lock<std::mutex> lock(mu_);
                cv_.wait(lock, [this] { return !queue_.empty(); });
                job = std::move(queue_.front());
                queue_.pop_front();
            }
            job->run();
        }
    }

    std::mutex mu_;
    std::condition_variable cv_;
    std::deque<std::shared_ptr<Job>> queue_;
    std::vector<std::thread> threads_;
};

}  // namespace

void SetParallelEnabled(bool on) { g_enabled.store(on, std::memory_order_relaxed); }
bool ParallelEnabled() { return g_enabled.load(std::memory_order_relaxed); }

void ParallelFor(int n, const std::function<void(int)>& fn) {
    if (n <= 0) return;
    if (n == 1 || t_inPool || !ParallelEnabled()) {
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
    job->fn = &fn;
    pool.submit(job, helpers);
    job->run();  // 调用线程也是 worker
    {
        std::unique_lock<std::mutex> lock(job->mu);
        job->cv.wait(lock, [&] { return job->done.load(std::memory_order_acquire) == n; });
    }
    if (job->error) std::rethrow_exception(job->error);
}

void ParallelInvoke(const std::function<void()>& a, const std::function<void()>& b) {
    ParallelFor(2, [&](int i) {
        if (i == 0) a();
        else b();
    });
}

}  // namespace nt
