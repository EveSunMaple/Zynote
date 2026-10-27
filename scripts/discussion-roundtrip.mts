import {spawn} from 'node:child_process'
import {mkdtemp, readFile, rm} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {dirname, join, resolve} from 'node:path'
import {fileURLToPath} from 'node:url'
import {
 discussionDigest,
 DiscussionStore,
 discussionPolicy,
 type DiscussionPack
} from '../src/main/media/bilibili/discussion-evidence.ts'

const projectRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const work = await mkdtemp(join(tmpdir(), 'zynote-discussion-'))
const mediaRoot = join(work, 'MediaDownloads')

const spawnGo = (args: string[], options: Parameters<typeof spawn>[2]) => spawn('go', args, options) // NOSONAR: CI/dev script with trusted runner Go.
const runGo = (env: Record<string, string>) =>
 new Promise<void>((resolvePromise, reject) => {
  const child = spawnGo(['test', './internal/media/bilibili', '-run', 'TestDiscussionCrossLanguage', '-count=1'], {
   cwd: join(projectRoot, 'backend'),
   env: {...process.env, ...env},
   stdio: 'inherit'
  })
  child.on('error', reject)
  child.on('exit', code => (code === 0 ? resolvePromise() : reject(new Error('Go 侧讨论证据测试失败：' + code))))
 })

const result = async () => JSON.parse(await readFile(join(work, 'go-result.json'), 'utf8'))

const text = '这里说错了，应该是「五」不是三。\n补充：<参考> & 出处'
const replyText = '同意，补充一个反例。'
const createdAt = 1700000000
const pack: DiscussionPack = {
 schema: 'video-discussion-v1',
 bvid: 'BV1xx411c7mD',
 cid: 99,
 page: 1,
 fetched_at: createdAt,
 classification_method: 'heuristic_candidates_v1',
 policy: discussionPolicy,
 coverage: {
  comments: {status: 'complete', complete: true, errors: []},
  danmaku: {
   status: 'partial',
   complete: false,
   errors: [],
   segments_total: 1,
   segments_requested: [1],
   segments_ok: [1]
  },
  items_truncated: false
 },
 items: [
  {
   id: 'comment:1',
   kind: 'comment',
   text,
   text_sha256: discussionDigest(text),
   labels: ['correction', 'supplement'],
   score: 9.5,
   selected: false,
   verification: 'unverified',
   reviewed: false,
   note: '',
   url: 'https://www.bilibili.com/video/BV1xx411c7mD#reply1',
   author: '观众',
   likes: 7,
   is_uploader: false,
   pinned: false,
   created_at: createdAt,
   root_id: '',
   parent_id: '',
   reply_count: 1,
   replies_complete: true
  },
  {
   id: 'reply:2',
   kind: 'reply',
   text: replyText,
   text_sha256: discussionDigest(replyText),
   labels: ['correction'],
   score: 1,
   selected: false,
   verification: 'unverified',
   reviewed: false,
   note: '',
   url: 'https://www.bilibili.com/video/BV1xx411c7mD#reply2',
   author: '观众',
   likes: 7,
   is_uploader: false,
   created_at: createdAt,
   root_id: 'comment:1',
   parent_id: 'comment:1'
  }
 ]
}

try {
 await runGo({ZYNOTE_DISCUSSION_DIR: work, ZYNOTE_DISCUSSION_MODE: 'save'})
 const goResult = await result()
 if (goResult.digest !== discussionDigest(text))
  throw new Error('跨语言 text_sha256 不一致：' + goResult.digest + ' vs ' + discussionDigest(text))
 const selected = new DiscussionStore(mediaRoot).select(goResult.ref, [
  {id: 'comment:1', selected: true, label: 'correction', note: '跨语言备注'}
 ])
 if (!selected.text.includes('跨语言备注') || !selected.text.includes('重要 · 主评论'))
  throw new Error('TypeScript 无法读取 Go 生成的讨论证据')

 const saved = new DiscussionStore(mediaRoot).save(pack)
 await runGo({
  ZYNOTE_DISCUSSION_DIR: work,
  ZYNOTE_DISCUSSION_MODE: 'select',
  ZYNOTE_DISCUSSION_REF: saved.ref
 })
 const goSelected = await result()
 if (
  !goSelected.text.includes('跨语言备注') ||
  !goSelected.text.includes('重要 · 主评论') ||
  goSelected.ref === saved.ref
 )
  throw new Error('Go 无法读取 TypeScript 生成的讨论证据')

 console.log('ok discussion roundtrip', goResult.ref.slice(0, 12), saved.ref.slice(0, 12))
} finally {
 await rm(work, {recursive: true, force: true})
}
