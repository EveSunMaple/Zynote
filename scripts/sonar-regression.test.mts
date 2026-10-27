import {strict as assert} from 'node:assert'
import {mkdtemp} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join, resolve} from 'node:path'
import {test} from 'node:test'
import {pathToFileURL} from 'node:url'
import {build} from 'esbuild'
import {scriptTokens} from '../src/main/im/weixin/im-weixin-html.ts'
import {isDiscussionNoise} from '../src/main/media/bilibili/discussion-evidence.ts'
import {windowsArgument} from '../src/main/security/windows-native.ts'
import {stripCodeFence} from '../src/shared/code-fence.ts'
import {htmlToText} from '../src/shared/html-text.ts'
import {methodFrontMatter} from '../src/shared/method-front-matter.ts'
import {answerChoices, compareChoices, questionOptions, questionStem} from '../src/shared/practice.ts'
import {stripLeading, stripTrailing} from '../src/shared/text-trim.ts'
import {inspectArchiveListing} from './media-source-prepare.mts'
import {containsPath, requireInside} from './path-guard.mts'
import {selectedMemberSize} from './video-candidate-package.mts'

const root = resolve('/tmp/zynote-path-guard')
const directory = await mkdtemp(join(tmpdir(), 'zynote-sonar-regression-')),
 bundles = {
  mediaSort: join(directory, 'media-sort.mjs'),
  srt: join(directory, 'srt.mjs'),
  subtitle: join(directory, 'subtitle.mjs'),
  courseGroups: join(directory, 'course-groups.mjs')
 },
 transcriptBundle = join(directory, 'transcript.cjs'),
 hlsBundle = join(directory, 'media-hls.cjs')
for (const [id, entry] of [
 ['mediaSort', 'src/main/media/core/media-sort.ts'],
 ['srt', 'src/shared/srt.ts'],
 ['subtitle', 'src/main/media/core/media-subtitle-download.ts'],
 ['courseGroups', 'src/shared/course-groups.ts']
] as const)
 // biome-ignore format: NOSONAR must stay on the await line
 await build({ // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
  entryPoints: [resolve(entry)],
  outfile: bundles[id],
  bundle: true,
  platform: 'node',
  format: 'esm'
 })
for (const [entry, outfile] of [
 ['src/main/documents/transcript.ts', transcriptBundle],
 ['src/main/media/transfer/media-hls.ts', hlsBundle]
] as const)
 // biome-ignore format: NOSONAR must stay on the await line
 await build({ // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
  entryPoints: [resolve(entry)],
  outfile,
  bundle: true,
  platform: 'node',
  format: 'cjs'
 })
const {sortMediaStreams} = await import(pathToFileURL(bundles.mediaSort).href)
const {parseSrt} = await import(pathToFileURL(bundles.srt).href)
const {nativeVttCues} = await import(pathToFileURL(bundles.subtitle).href)
const {courseSubject} = await import(pathToFileURL(bundles.courseGroups).href)
const transcriptModule = await import(pathToFileURL(transcriptBundle).href),
 splitTranscript = transcriptModule.splitTranscript ?? transcriptModule.default.splitTranscript,
 hlsModule = await import(pathToFileURL(hlsBundle).href),
 parseHls = hlsModule.parseHls ?? hlsModule.default.parseHls

test('path guard accepts the root and descendants', () => {
 assert.equal(containsPath(root, root), true)
 assert.equal(containsPath(root, join(root, 'a', 'b')), true)
 assert.equal(requireInside(root, join(root, 'a'), 'test'), join(root, 'a'))
})

test('path guard rejects traversal, sibling prefixes and absolute escapes', () => {
 assert.equal(containsPath(root, join(root, '..', 'x')), false)
 assert.equal(containsPath(root, join(root, '..', 'zynote-path-guard-evil')), false)
 assert.equal(containsPath(root, resolve('/tmp/zynote-path-guard-evil')), false)
 assert.throws(() => requireInside(root, join(root, '..', 'x'), 'test'), /must stay inside/)
})

test('choice ordering stays code-unit stable across locales', () => {
 assert.deepEqual(answerChoices('参考答案：B, A', ['A', 'B', 'C']), ['A', 'B'])
 assert.equal(compareChoices('B,A', ['A', 'B']), true)
 assert.deepEqual(
  questionOptions('A. 甲\nB. 乙').map(option => option.key),
  ['A', 'B']
 )
})

test('question parsing keeps options, answers and stems', () => {
 const stem = '题干\nA. 甲\n继续甲\nB、乙\nC)丙'
 assert.deepEqual(questionOptions(stem), [
  {key: 'A', text: '甲\n继续甲'},
  {key: 'B', text: '乙'},
  {key: 'C', text: '丙'}
 ])
 assert.equal(questionStem(stem), '题干')
 assert.equal(questionStem('A. 甲\nB. 乙'), 'A. 甲\nB. 乙')
 assert.deepEqual(answerChoices('参考答案：A、B', ['A', 'B']), ['A', 'B'])
 assert.deepEqual(answerChoices('A,B1', ['A', 'B']), ['A'])
 assert.deepEqual(answerChoices('A1B', ['A', 'B']), [])
})

test('codec ranking only matches known prefixes', () => {
 const rows = ['xxav1', 'avc1', 'hev1', 'hvc1', 'vp9', 'av01.0', 'av1.0', 'xxh264'].map((codec, index) => ({
  id: String(index),
  kind: 'video' as const,
  urls: [],
  height: 1080,
  codec,
  bandwidth: 1000,
  extension: 'mp4'
 }))
 assert.deepEqual(
  sortMediaStreams(rows, 'vcodec').map((row: {codec: string}) => row.codec),
  ['av01.0', 'av1.0', 'vp9', 'hev1', 'hvc1', 'avc1', 'xxav1', 'xxh264']
 )
})

test('text trimming preserves endpoint, filename and domain semantics', () => {
 assert.equal(stripTrailing('https://huggingface.co///', '/'), 'https://huggingface.co')
 assert.equal(stripTrailing('report... ', '. '), 'report')
 assert.equal(stripTrailing(stripLeading('..example.com....', '.'), '.'), 'example.com')
 const long = 'a'.repeat(100_000) + '. '.repeat(10_000)
 const start = performance.now()
 assert.equal(stripTrailing(long, '. '), 'a'.repeat(100_000))
 assert.ok(performance.now() - start < 2000)
})

test('code fence stripping preserves JSON content', () => {
 assert.equal(stripCodeFence('```json\n{"a":1}\n```', 'json'), '{"a":1}')
 assert.equal(stripCodeFence('```\n{"a":1}\n```', 'json'), '{"a":1}')
 assert.equal(stripCodeFence('{"a":1}', 'json'), '{"a":1}')
 assert.equal(stripCodeFence('```JSON\n{"a":1}\n```', 'json'), '{"a":1}')
 const long = '```json\n' + 'a'.repeat(100_000) + '\n```'
 const start = performance.now()
 assert.equal(stripCodeFence(long, 'json'), 'a'.repeat(100_000))
 assert.ok(performance.now() - start < 2000)
})

test('SRT fence extraction matches unfenced parsing on long input', () => {
 const srt = '1\n00:00:00,000 --> 00:00:01,000\n你好\n'
 assert.deepEqual(parseSrt('```srt\n' + srt + '```'), parseSrt(srt))
 const long = 'x'.repeat(100_000) + '\n```srt\n' + srt + '```'
 const start = performance.now()
 assert.deepEqual(parseSrt(long), parseSrt(srt))
 assert.ok(performance.now() - start < 2000)
})

test('HTML text extraction drops script/style and keeps visible text', () => {
 assert.equal(htmlToText('<p>你好&amp;世界</p><script>alert(1)</script><style>p{}</style>'), '你好&世界')
 assert.equal(htmlToText('foo<b>bar</b>'), 'foo bar')
 const long = '<script>' + 'x'.repeat(100_000) + '</script><p>' + 'y'.repeat(100_000) + '</p>'
 const start = performance.now()
 assert.equal(htmlToText(long), 'y'.repeat(100_000))
 assert.ok(performance.now() - start < 2000)
})

test('tar verbose listings parse without backtracking', () => {
 const names = 'root/\nroot/a.txt',
  details = 'drwxr-xr-x  2 user group 0 Jan  1 12:00 root/\n-rw-r--r--  1 user group 5 Jan  1 12:00 root/a.txt',
  listing = inspectArchiveListing(names, details, 'root')
 assert.equal(listing.total, 5)
 assert.equal(listing.entries.get('root/a.txt')?.size, 5)
 const name = 'root/' + 'a'.repeat(100_000),
  line = '-rw-r--r--  1 user group 5 Jan  1 12:00 ' + name,
  start = performance.now()
 assert.equal(selectedMemberSize(line, name), 5)
 assert.ok(performance.now() - start < 2000)
})

test('transcript and WebVTT parsing keeps timestamps', () => {
 const transcript = splitTranscript('0:01 你好\n1:02:03 世界\n普通文本')
 assert.deepEqual(
  transcript.segments.map((segment: {text: string}) => segment.text),
  ['你好', '世界', '普通文本']
 )
 const vtt = nativeVttCues('WEBVTT\n\n00:00:00.000 --> 00:00:01.000\n你好\n\n00:00:02.000 --> 00:00:03.000\n世界')
 assert.deepEqual(
  vtt.body.map((cue: {content: string}) => cue.content),
  ['你好', '世界']
 )
})

test('course subject strips only a trailing year range', () => {
 assert.equal(courseSubject('高等数学 2023 - 2024'), '高等数学')
 assert.equal(courseSubject('高等数学（上）'), '高等数学（上）')
})

test('method front matter keeps CRLF, BOM and body intact', () => {
 const front = methodFrontMatter('\ufeff---\r\nname: test\r\ndescription: desc\r\n---\r\n正文\r\n第二行')
 assert.equal(front?.header.replaceAll('\r\n', '\n'), 'name: test\ndescription: desc\n')
 assert.equal(front?.body, '正文\r\n第二行')
 assert.equal(methodFrontMatter('---\n---\nbody'), undefined)
})

test('HLS attribute lists handle quoted commas', () => {
 const parsed = parseHls(
  '#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="a,b",NAME="x",DEFAULT=YES,URI="audio.m3u8"\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:1.0,\nseg.ts\n#EXT-X-ENDLIST',
  'https://example.com/v/index.m3u8',
  (url: string) => url
 )
 assert.deepEqual(parsed.audio, [{url: 'https://example.com/v/audio.m3u8', group: 'a,b', preferred: true}])
 assert.equal(parsed.segments[0].url, 'https://example.com/v/seg.ts')
 const longName = 'x'.repeat(100_000),
  start = performance.now()
 parseHls(
  '#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="g",NAME="' +
   longName +
   '",DEFAULT=YES,URI="a.m3u8"\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:1,\ns.ts\n#EXT-X-ENDLIST',
  'https://example.com/v/index.m3u8',
  (url: string) => url
 )
 assert.ok(performance.now() - start < 2000)
})

test('weixin script tokenizer keeps strings and comments intact', () => {
 assert.deepEqual(scriptTokens('// c\nvar s = "a\\"b"; /* x */ 12 + id'), [
  'var',
  's',
  '=',
  '"a\\"b"',
  ';',
  '12',
  '+',
  'id'
 ])
 const long = 'a'.repeat(100_000),
  start = performance.now()
 assert.deepEqual(scriptTokens(long), [long])
 assert.ok(performance.now() - start < 2000)
})

test('Windows argument quoting matches CommandLineToArgvW', () => {
 assert.equal(windowsArgument(''), '')
 assert.equal(windowsArgument('plain'), 'plain')
 assert.equal(windowsArgument('a b'), '"a b"')
 assert.equal(windowsArgument('a"b'), '"a\\"b"')
 assert.equal(windowsArgument('a b\\'), '"a b\\\\"')
 assert.equal(windowsArgument('a\\"b'), '"a\\\\\\"b"')
 assert.throws(() => windowsArgument('a\0b'), /空字符/)
 const long = 'a b '.repeat(25_000),
  start = performance.now()
 assert.ok(windowsArgument(long).length > long.length)
 assert.ok(performance.now() - start < 2000)
})

test('discussion noise keeps the original semantics', () => {
 for (const text of [
  '前排',
  '前排 沙发',
  '666666',
  '哈哈哈哈',
  '[doge]',
  '[前排][doge] 666',
  '   ',
  '打卡签到路过',
  '已三连'
 ])
  assert.equal(isDiscussionNoise(text), true, text)
 for (const text of ['正常讨论', '前排但我觉得不对', '已三连也不代表没问题', '[doge] 这个观点有误'])
  assert.equal(isDiscussionNoise(text), false, text)
})

test('discussion noise stays linear on 100k-character inputs', () => {
 const inputs = ['[' + 'a'.repeat(100_000), '前排'.repeat(25_000), 'a'.repeat(100_000), '['.repeat(100_000)]
 for (const text of inputs) {
  const start = performance.now()
  isDiscussionNoise(text)
  const elapsed = performance.now() - start
  assert.ok(elapsed < 2000, 'noise scan took ' + elapsed.toFixed(1) + 'ms')
 }
})
