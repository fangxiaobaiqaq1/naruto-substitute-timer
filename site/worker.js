// Workers 静态资源不支持 Range 请求（总是返回整个文件），而 iPhone/Mac 的 Safari 必须用 Range 才肯播放视频和音频。
// 只有 /media/* 先进这里（wrangler.jsonc 的 run_worker_first）：取整个文件，按 Range 切一段返回 206；其他路径直接走静态资源。
// 经过这里的请求计入免费版每天 10 万次的 Worker 额度，超了视频和音乐会失效，页面只剩海报。
// ponytail: 每个 Range 请求都把整个文件读进内存，媒体超过几十 MB 时改放 R2（原生支持 Range）。

export function parseRange(header, size) {
  const m = /^bytes=(\d*)-(\d*)$/.exec(header || '')
  if (!m || (m[1] === '' && m[2] === '')) return null // 没有或看不懂（含多段）：返回整个文件
  const start = m[1] === '' ? Math.max(size - Number(m[2]), 0) : Number(m[1])
  const end = m[1] === '' || m[2] === '' ? size - 1 : Math.min(Number(m[2]), size - 1)
  return start <= end ? [start, end] : 'unsatisfiable'
}

export default {
  async fetch(request, env) {
    const res = await env.ASSETS.fetch(request)
    const headers = new Headers(res.headers)
    headers.set('Accept-Ranges', 'bytes')
    const range = request.method === 'GET' && res.status === 200 && request.headers.get('Range')
    if (!range) return new Response(res.body, { status: res.status, headers })

    const buf = await res.arrayBuffer()
    const r = parseRange(range, buf.byteLength)
    if (!r) return new Response(buf, { status: 200, headers })
    if (r === 'unsatisfiable') {
      return new Response(null, { status: 416, headers: { 'Content-Range': `bytes */${buf.byteLength}` } })
    }
    const [start, end] = r
    headers.set('Content-Range', `bytes ${start}-${end}/${buf.byteLength}`)
    headers.set('Content-Length', String(end - start + 1))
    return new Response(buf.slice(start, end + 1), { status: 206, headers })
  },
}
