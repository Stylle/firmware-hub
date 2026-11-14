// Wait for the imported database to invalidate the old browser session.
export async function waitForRestoredSession(url, signal, timeout = 120000) {
 const deadline = Date.now() + timeout
 while (Date.now() < deadline) {
  if (signal.aborted) return false
  const request = new AbortController()
  const abort = () => request.abort()
  signal.addEventListener('abort', abort, { once: true })
  const timer = setTimeout(abort, 3000)
  try {
   const response = await fetch(url, {credentials:'same-origin', cache:'no-store',signal:request.signal})
   if (response.status === 401) return true
   if (response.ok && (await response.json()).authenticated === false) return true
  } catch { /* Restart may briefly refuse connections. */ }
  finally { clearTimeout(timer); signal.removeEventListener('abort',abort) }
  await new Promise(resolve => setTimeout(resolve,1000))
 }
 throw new Error('服务重新加载耗时较长。请稍后刷新页面，勿重复导入。')
}

