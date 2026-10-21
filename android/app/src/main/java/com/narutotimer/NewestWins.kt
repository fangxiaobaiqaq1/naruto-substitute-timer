package com.narutotimer

import java.util.concurrent.Executor

/**
 * Serial background jobs where only the newest request counts (pure JVM, checked on the host by
 * android/tools/appcheck). Used for engine (re)loads (audit AND-12): two quick identity imports
 * used to start two loader threads, the first published READY while the second was about to
 * reset the pipeline.
 *
 * [submit] starts a new generation. Jobs run one at a time on [executor] (which must be serial);
 * a job that is already superseded when its turn comes is skipped, and a job superseded while it
 * ran does not publish — the newer one, queued behind it, will.
 */
class NewestWins(private val executor: Executor) {
    private val lock = Any()
    private var generation = 0L

    /**
     * [begin] runs at once on the caller, under the gate's lock (e.g. publish "loading"); [work]
     * runs on the executor and must not throw; [publish] gets its result under the lock, only if
     * no newer request was submitted meanwhile. Returns the request's generation.
     */
    fun <T> submit(begin: () -> Unit, work: () -> T, publish: (T) -> Unit): Long {
        val gen = synchronized(lock) {
            begin()
            ++generation
        }
        executor.execute {
            if (isCurrent(gen)) {
                val result = work()
                synchronized(lock) { if (gen == generation) publish(result) }
            }
        }
        return gen
    }

    fun isCurrent(gen: Long): Boolean = synchronized(lock) { gen == generation }
}
