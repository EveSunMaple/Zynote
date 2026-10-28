export function bilibiliUrl(value: string) {
 const u = new URL(value.trim())
 if (
  u.protocol !== 'https:' ||
  !['www.bilibili.com', 'bilibili.com'].includes(u.hostname) ||
  !/^\/video\/(BV[\w]+|av\d+)\/?$/.test(u.pathname) || // NOSONAR: modifier is required by the public component contract.
  u.username ||
  u.password
 )
  throw new Error('请输入完整的 B 站视频网址，例如 https://www.bilibili.com/video/BV…/?p=5')
 const p = u.searchParams.get('p')
 if (p && !/^[1-9]\d*$/.test(p)) throw new Error('视频分 P 编号无效')
 u.hash = ''
 return u.href
}
export function videoMediaUrl(value: string) {
 try {
  const u = new URL(value)
  return (
   u.protocol === 'https:' &&
   !u.username &&
   !u.password &&
   (!u.port || u.port === '443') &&
   /(^|\.)bilivideo\.com$/.test(u.hostname) &&
   /\.mp4$/i.test(u.pathname)
  )
 } catch {
  return false
 }
}
