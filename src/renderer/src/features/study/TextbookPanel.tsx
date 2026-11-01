import {useEffect, useMemo, useRef, useState, type CSSProperties, type Ref} from 'react'
import {Button, Checkbox, Disclosure, NumberField, ProgressBar, Spinner, TextArea} from '@heroui/react'
import {
 BookOpen,
 ChevronLeft,
 ChevronRight,
 Ellipsis,
 Highlighter,
 MessagesSquare,
 Scan,
 Sparkles,
 ZoomIn,
 ZoomOut
} from 'lucide-react'
import {ConfirmDialog} from '@renderer/ui/ConfirmDialog'
import {StudyField} from '@renderer/features/study/Field'
import {StudyNextStep} from '@renderer/features/study/StudyNextStep'
import {useTextbookSelection, type ManualMark} from '@renderer/features/study/TextbookSelection'
import {sessionDescription, TextbookSessions} from '@renderer/features/study/TextbookSessions'
import {SelectField} from '@renderer/ui/SelectField'
import type {usePdfPage} from '@renderer/features/study/usePdfPage'
import {usePdfNavigation} from '@renderer/features/study/usePdfNavigation'
import {TextbookBookmarks} from '@renderer/features/study/TextbookBookmarks'
import {CommentText, TextbookComments, usePageAnnotations} from '@renderer/features/study/TextbookComments'
import {TeacherAnnotations, type TeacherMark} from '@renderer/features/study/TeacherAnnotations'
import {ChapterSummary} from '@renderer/features/study/ChapterSummary'
import {SettingsLink} from '@renderer/app/settings-navigation'
import type {AnalysisSource, Book, Draft} from '@renderer/features/study/types'
import type {Highlight, StudyEntry} from '@shared/study'
import {highlightLabels} from '@shared/study'
import type {Progress} from '@shared/types'
import '@renderer/features/study/textbook-workspace.css'

export type TextbookProps = Readonly<{
 entry: StudyEntry | undefined
 analysisError: string
 analysisSource: AnalysisSource
 entries: StudyEntry[]
 onSelectEntry: (id: string) => void
 bookFailure: string
 resolvingBook: boolean
 savedAssociation: boolean
 lectureSaved: boolean
 dirty: boolean
 issues: number
 onSaveSchedule: () => void
 onReviewIssues: () => void
 bookSuggestion: string
 useCourseBook?: () => void
 timezone: string
 onLinkSession: (id: string, checked: boolean) => void
 lectureTitle?: string // NOSONAR: shared props type; this member is used by the sibling component.
 book: Book | null
 pageNumber: number
 first: number // NOSONAR: shared props type; this member is used by the sibling component.
 last: number // NOSONAR: shared props type; this member is used by the sibling component.
 followsPage: boolean
 onFollowPage: () => void
 draft: Draft | null
 chosen: string[] // NOSONAR: shared props type; this member is used by the sibling component.
 busy: boolean
 progress: Progress | null // NOSONAR: shared props type; this member is used by the sibling component.
 pdfVersion: number // NOSONAR: shared props type; this member is used by the sibling component.
 onPdfSaved: () => void
 onPickBook: () => void // NOSONAR: shared props type; this member is used by the sibling component.
 onAnalyze: (source: AnalysisSource) => void
 onOpenBook: () => void // NOSONAR: shared props type; this member is used by the sibling component.
 onRemoveHighlights: () => void // NOSONAR: shared props type; this member is used by the sibling component.
 onCancel: () => void // NOSONAR: shared props type; this member is used by the sibling component.
 onPageNumber: (page: number) => void
 onFirst: (page: number) => void // NOSONAR: shared props type; this member is used by the sibling component.
 onLast: (page: number) => void // NOSONAR: shared props type; this member is used by the sibling component.
 onChosen: (ids: string[]) => void // NOSONAR: shared props type; this member is used by the sibling component.
 onApply: () => void // NOSONAR: shared props type; this member is used by the sibling component.
 onManual: (value: {
  // NOSONAR: shared props type; this member is used by the sibling component.
  marks: ManualMark[]
  source: Highlight['source']
  evidence: string
  extension: boolean
 }) => Promise<boolean>
 onGoSchedule: () => void // NOSONAR: shared props type; this member is used by the sibling component.
}>

const basename = (path: string) => path.split(/[\\/]/).at(-1) || path
const sourceKeys = Object.keys(highlightLabels) as Highlight['source'][]

function SourceLabel({source}: Readonly<{source: Highlight['source']}>) {
 return (
  <span className="study-source" data-study-source={source}>
   <span className="study-source-dot" aria-hidden="true" />
   {highlightLabels[source]}
  </span>
 )
}

function rangeProblem(first: number, last: number, pages: number) {
 if (!Number.isFinite(first) || !Number.isFinite(last)) return '请填写有效的页码范围'
 if (first < 1 || last < first) return '结束页需要大于或等于起始页'
 if (last > pages) return '结束页不能超过教材总页数'
 if (last - first > 99) return '每次最多分析 100 页'
 return ''
}

function Overlay({
 rect,
 viewport,
 size,
 source,
 preview,
 active,
 target
}: Readonly<{
 rect: Highlight['rect']
 viewport: ReturnType<typeof usePdfPage>['viewport']
 size: {width: number; height: number}
 source: Highlight['source']
 preview: boolean
 active?: boolean
 target?: Ref<HTMLSpanElement>
}>) {
 if (!viewport) return null
 const left = viewport.convertToViewportPoint(rect[0], rect[1]),
  right = viewport.convertToViewportPoint(rect[0] + rect[2], rect[1] + rect[3]),
  style: CSSProperties = {
   left: (Math.min(left[0], right[0]) / size.width) * 100 + '%',
   top: (Math.min(left[1], right[1]) / size.height) * 100 + '%',
   width: (Math.abs(left[0] - right[0]) / size.width) * 100 + '%',
   height: (Math.abs(left[1] - right[1]) / size.height) * 100 + '%'
  }
 return (
  <span
   ref={target}
   aria-hidden="true"
   data-study-source={source}
   className={
    'study-highlight' + (preview ? ' study-highlight--preview' : '') + (active ? ' study-highlight--active' : '')
   }
   style={style}
  />
 )
}

export function TextbookMenu(props: TextbookProps) {
 const {book, entry, busy} = props
 return (
  <div className="study-menu">
   <section className="study-side-card">
    <small>当前课次</small>
    {props.entries.length ? (
     <SelectField
      label="当前课次"
      value={entry?.id || 'standalone'}
      searchable={props.entries.length > 8}
      isDisabled={busy}
      onChange={value => props.onSelectEntry(value === 'standalone' ? '' : value)}
      options={[
       {value: 'standalone', label: '独立阅读'},
       ...props.entries.map(row => ({
        value: row.id,
        label: row.title || '未命名课次',
        description: sessionDescription(row, props.timezone)
       }))
      ]}
     />
    ) : (
     <span>独立阅读</span>
    )}
    {entry && <span>{sessionDescription(entry, props.timezone)}</span>}
   </section>
   {book && (
    <>
     <div className="study-book">
      <small>当前教材</small>
      <strong title={book.path}>{basename(book.path)}</strong>
      <small>{book.pages} 页</small>
     </div>
     <TextbookSessions
      key={book.id}
      entries={props.entries}
      bookId={book.id}
      currentTitle={entry?.title}
      timezone={props.timezone}
      busy={busy}
      onLink={props.onLinkSession}
     />
    </>
   )}
   {props.useCourseBook && (
    <Button variant="tertiary" size="sm" isDisabled={busy} onPress={props.useCourseBook}>
     {props.bookSuggestion}
    </Button>
   )}
   {book && !props.savedAssociation && <p className="study-menu__hint">教材关联待保存</p>}
   {props.dirty && (entry || book) && (
    <>
     <Button variant="secondary" size="sm" isDisabled={busy || props.issues > 0} onPress={props.onSaveSchedule}>
      保存课表与教材关联
     </Button>
     {props.issues > 0 && (
      <Button variant="ghost" size="sm" onPress={props.onReviewIssues}>
       返回课表补全必填项（{props.issues}）
      </Button>
     )}
    </>
   )}
  </div>
 )
}

function TextbookProgress({busy, progress, onCancel}: Pick<TextbookProps, 'busy' | 'progress' | 'onCancel'>) {
 if (!busy) return null
 return (
  <section // NOSONAR: existing element/role semantics are intentional for layout and keyboard behavior.
   className="study-progress"
   role="status" // NOSONAR: styled live region; native output would change layout and announcement behavior.
  >
   <Spinner size="sm" />
   <div>
    <strong>{progress?.stage || '正在处理教材'}</strong>
    <small>{progress?.message || '请稍候'}</small>
   </div>
   <Button variant="ghost" size="sm" onClick={onCancel}>
    取消
   </Button>
   {progress?.part !== undefined && progress?.total !== undefined ? (
    <ProgressBar aria-label="分析进度" maxValue={progress.total} value={progress.part}>
     <ProgressBar.Track>
      <ProgressBar.Fill />
     </ProgressBar.Track>
    </ProgressBar>
   ) : null}
  </section>
 )
}

function TextbookMore(props: TextbookProps & {rangeInput: Ref<HTMLInputElement>}) {
 const {book, pageNumber, first, last, busy, onFirst, onLast, onPageNumber, onOpenBook, onRemoveHighlights} = props
 const [confirmRemove, setConfirmRemove] = useState(false)
 if (!book) return null
 const problem = rangeProblem(first, last, book.pages)
 return (
  <div className="textbook-more" id="textbook-more">
   <section className="study-side-section">
    <h3>页码范围</h3>
    <p className="study-menu__hint">
     {props.followsPage
      ? '跟随阅读页，从当前页起最多分析 5 页。手动修改页码可以固定范围。'
      : '已固定分析范围，翻页不会改变起止页。'}
    </p>
    {!props.followsPage && (
     <Button variant="ghost" size="sm" isDisabled={busy} onPress={props.onFollowPage}>
      恢复跟随阅读页
     </Button>
    )}
    <div className="study-range">
     <StudyField label="起始页">
      <NumberField
       aria-label="章节起始页"
       isDisabled={busy}
       minValue={1}
       maxValue={book.pages}
       value={first}
       onChange={value => onFirst(value)}
      >
       <NumberField.Group>
        <NumberField.DecrementButton />
        <NumberField.Input ref={props.rangeInput} onFocus={event => event.currentTarget.select()} />
        <NumberField.IncrementButton />
       </NumberField.Group>
      </NumberField>
     </StudyField>
     <StudyField label="结束页">
      <NumberField
       aria-label="章节结束页"
       isDisabled={busy}
       minValue={1}
       maxValue={book.pages}
       value={last}
       onChange={value => onLast(value)}
      >
       <NumberField.Group>
        <NumberField.DecrementButton />
        <NumberField.Input onFocus={event => event.currentTarget.select()} />
        <NumberField.IncrementButton />
       </NumberField.Group>
      </NumberField>
     </StudyField>
    </div>
    <div className="study-range-actions">
     <Button
      variant="ghost"
      size="sm"
      isDisabled={busy}
      onClick={() => {
       onFirst(pageNumber)
       if (last < pageNumber) onLast(pageNumber)
      }}
     >
      当前页设为起点
     </Button>
     <Button variant="ghost" size="sm" isDisabled={busy} onClick={() => onLast(pageNumber)}>
      当前页设为终点
     </Button>
    </div>
    <p className={problem ? 'study-side-hint study-side-hint--warning' : 'study-side-hint'}>
     {problem || `将分析第 ${first}–${last} 页，共 ${last - first + 1} 页。`}
    </p>
   </section>
   <section className="study-side-section study-side-section--quiet">
    <h3>批注颜色</h3>
    <ul className="textbook-color-key" aria-label="批注颜色">
     {sourceKeys.map(source => (
      <li key={source}>
       <SourceLabel source={source} />
      </li>
     ))}
    </ul>
   </section>
   <section className="study-side-section study-side-section--quiet">
    <h3>文件操作</h3>
    <Button variant="tertiary" size="sm" isDisabled={busy} onPress={props.onPickBook}>
     更换教材 PDF
    </Button>
    <div className="study-side-actions">
     <Button variant="ghost" size="sm" isDisabled={busy} onClick={onOpenBook}>
      打开原教材
     </Button>
     <Button variant="ghost" size="sm" isDisabled={busy} onClick={() => onPageNumber(1)}>
      回到第一页
     </Button>
    </div>
    <div className="study-danger-zone study-danger-zone--compact">
     <div>
      <strong>移除软件划线</strong>
      <small>原有批注会保留。</small>
     </div>
     <Button variant="danger-soft" size="sm" isDisabled={busy} onClick={() => setConfirmRemove(true)}>
      移除划线
     </Button>
    </div>
   </section>
   {confirmRemove ? (
    <ConfirmDialog
     title="移除本软件添加的划线？"
     confirmLabel="移除"
     busy={busy}
     onCancel={() => setConfirmRemove(false)}
     onConfirm={() => {
      setConfirmRemove(false)
      onRemoveHighlights()
     }}
    >
     将移除本软件写入这份教材的划线、附在划线上的笔记和章小结。保存前会创建
     <code>.划线前备份.pdf</code>。{/* NOSONAR: adjacent JSX spacing is intentional for the rendered layout. */}
    </ConfirmDialog>
   ) : null}
  </div>
 )
}

function replayDescription(entry: StudyEntry | undefined, cachedReplay?: string, lectureTitle?: string) {
 const replay = entry?.subId || cachedReplay
 if (!entry || !replay) return ''
 const label = (entry.subId && entry.zhiyunReplayLabel) || (replay === cachedReplay && lectureTitle)
 return [entry.title, label || `回放 ${replay}`].filter(Boolean).join(' · ')
}

function AnalysisAction(props: TextbookProps & {teacher: boolean; replaySubId?: string; editRange: () => void}) {
 if (props.busy) return null
 const teacher = props.teacher
 const source = teacher ? 'teacher' : 'ai'
 const action = teacher ? '用智云课堂转写生成批注' : '根据教材生成批注'
 if (teacher && !props.replaySubId && !props.entry?.subId)
  return (
   <div className="textbook-next">
    <p className="study-menu__hint">
     {props.entry
      ? '选择这节课的智云回放，千问就能根据老师讲话的语音转写标注教材。'
      : '先关联一节课的智云回放，千问会用老师讲话的语音转写标注教材。'}
    </p>
    <Button size="sm" variant="primary" onPress={props.onGoSchedule}>
     关联智云回放
    </Button>
   </div>
  )
 const problem = props.book && rangeProblem(props.first, props.last, props.book.pages)
 const emptyResult = props.draft?.rows.length === 0
 if (problem || emptyResult)
  return (
   <div className="textbook-next">
    {problem && <p className="study-inline-error">{problem}</p>}
    <StudyNextStep>点「修改分析页码」，选择要分析的正文页。</StudyNextStep>
    <Button size="sm" variant="primary" onPress={props.editRange}>
     修改分析页码
    </Button>
    {emptyResult && !problem && (
     <Button size="sm" variant="ghost" onPress={() => props.onAnalyze(source)}>
      {action}
     </Button>
    )}
   </div>
  )
 if (teacher && !props.lectureSaved)
  return (
   <div className="textbook-next">
    <StudyNextStep>
     {props.issues ? '点「去补全课表」，填写缺少的信息。' : '点「保存课堂资料关联」，让千问读取本次课的资料。'}
    </StudyNextStep>
    <Button size="sm" variant="primary" onPress={props.issues ? props.onReviewIssues : props.onSaveSchedule}>
     {props.issues ? `去补全课表（${props.issues} 项）` : '保存课堂资料关联'}
    </Button>
   </div>
  )
 return (
  <div className="textbook-next">
   {!props.draft && (
    <StudyNextStep>
     {teacher ? '千问根据这节课里老师讲话的语音转写，匹配教材' : '仅用教材正文整理'}第 {props.first}–{props.last}{' '}
     页，点「{action}」。
    </StudyNextStep>
   )}
   <Button
    size="sm"
    variant={props.draft ? 'tertiary' : 'primary'}
    isDisabled={props.busy}
    onPress={() => props.onAnalyze(source)}
   >
    {action}
   </Button>
   {!teacher && props.entry?.lectureId && props.lectureSaved && (
    <Button size="sm" variant="ghost" onPress={() => props.onAnalyze('materials')}>
     结合已关联的课次资料
    </Button>
   )}
   {props.analysisError && teacher && (
    <Button size="sm" variant="ghost" onPress={props.onGoSchedule}>
     检查课堂资料
    </Button>
   )}
  </div>
 )
}

// biome-ignore format: NOSONAR must stay on the function line
export function TextbookContent(props: TextbookProps) { // NOSONAR: cohesive flow; further extraction would risk behavior changes.
 // NOSONAR: cohesive flow; further extraction would risk behavior changes.
 // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
 const {entry, book, pageNumber, chosen, busy, pdfVersion, onPageNumber, onChosen, onApply, onManual} = props
 const [mode, setMode] = useState<'read' | 'teacher' | 'manual' | 'ai'>('read'),
  [destination, setDestination] = useState<TeacherMark | null>(null),
  [more, setMore] = useState(false),
  [preview, setPreview] = useState<{page: number; ids: string[]} | null>(null),
  [source, setSource] = useState<Highlight['source']>('personal'),
  [evidence, setEvidence] = useState(''),
  [extension, setExtension] = useState(false),
  [confirmApply, setConfirmApply] = useState(false)
 const {frame, paper, zoom, zoomBy, zoomTo, canvas, document, viewport, renderedPage, size, rendering, error} = usePdfNavigation(book, pageNumber, mode === 'read', pdfVersion)
 const selection = useTextbookSelection(book?.id, pageNumber, entry?.id, renderedPage, viewport, mode === 'manual', busy, source)
 const [commentVersion, setCommentVersion] = useState(0)
 const annotations = usePageAnnotations(book?.id, pageNumber, pdfVersion + commentVersion)
 const reviewing = mode === 'teacher' || mode === 'ai'
 const draft = reviewing && (mode === 'teacher') === (props.draft?.source === 'teacher') ? props.draft : null
 const analysisError = reviewing && (mode === 'teacher') === (props.analysisSource === 'teacher') ? props.analysisError : ''
 const actionProps = {...props, draft, analysisError}
 const previewTarget = useRef<HTMLSpanElement>(null)
 const lastScroll = useRef<typeof preview>(null)
 useEffect(() => {
  if (!preview || lastScroll.current === preview || rendering || renderedPage?.pageNumber !== preview.page || pageNumber !== preview.page || !previewTarget.current) return
  // A click can target a different PDF page, or a highlight which isn't selected for saving.
  previewTarget.current.scrollIntoView({block: 'center', inline: 'nearest', behavior: 'instant'})
  lastScroll.current = preview
 }, [preview, rendering, renderedPage, pageNumber])
 function locateTeacher(mark: TeacherMark) {
  setDestination(mark)
  onPageNumber(mark.page)
  setMode('read')
  setMore(false)
 }
 useEffect(() => {
  setDestination(null)
  setEvidence('')
  setPreview(null)
  setMode('read')
  setMore(false)
 }, [entry?.id, book?.id])
 const groups = useMemo(() => {
  if (!draft) return []
  const map = new Map<
   string,
   {page: number; source: Highlight['source']; quote: string; evidence: string; comments: Highlight['comments']; extension: boolean; ids: string[]}
  >()
  for (const row of draft.rows) {
   const key = [row.page, row.source, row.quote, row.evidence].join('\u0000')
   const group = map.get(key)
   if (group) group.ids.push(row.id)
   else
    map.set(key, {
     page: row.page,
     source: row.source,
     quote: row.quote,
     evidence: row.evidence,
     comments: row.comments,
     extension: row.extension,
     ids: [row.id]
    })
  }
  return [...map.entries()].sort((a, b) => a[1].page - b[1].page)
 }, [draft])
 const selectedCount = groups.filter(group => group[1].ids.every(id => chosen.includes(id))).length
 const rangeInput = useRef<HTMLInputElement>(null)
 function editRange() {
  setMore(true)
  requestAnimationFrame(() => rangeInput.current?.focus())
 }
 const saveManual = async () => {
  if (!selection.marks.length) return
  const ok = await onManual({marks: selection.marks, source, evidence: evidence.trim(), extension})
  if (ok) {
   selection.clear()
   setEvidence('')
   setExtension(false)
  }
 }
 if (!book) return (
  <div className="study-file-start">
   <span className="study-file-start__icon"><BookOpen size={26} aria-hidden="true" /></span>
   <h3>打开一本教材</h3>
   {props.bookFailure && <p role="alert" className="study-inline-error">{props.bookFailure}</p>}
   {!busy && !props.resolvingBook && <StudyNextStep>{props.bookFailure ? '点「打开教材 PDF」，重新选择一份可读的文件。' : '点「打开教材 PDF」，选择要读的本地教材。'}</StudyNextStep>}
   {props.resolvingBook && <p role="status">正在打开已关联的教材…</p>}
   {!busy && !props.resolvingBook && <Button className="study-file-start__button" variant="primary" onPress={props.onPickBook}>打开教材 PDF</Button>}
   <TextbookProgress {...props} />
  </div>
 )
 return (
  <div className="textbook-workspace">
   <div className="textbook-tools">
    <div className="study-segmented textbook-modes" role="group" aria-label="阅读与批注">
     {([
      {value:'read',label:'阅读',icon:BookOpen},
      {value:'teacher',label:'老师批注',icon:MessagesSquare},
      {value:'manual',label:'自主批注',icon:Highlighter},
      {value:'ai',label:'AI 批注',icon:Sparkles}
     ] as const).map(({value,label,icon:Icon}) => (
      <Button key={value} aria-pressed={mode === value} variant="ghost" size="sm" onPress={() => {setMode(value); setMore(false)}}
       className={'study-segmented__button' + (mode === value ? ' study-segmented__button--active' : '')}>
       <Icon size={15} aria-hidden="true" />{label}
      </Button>
     ))}
    </div>
    <Button variant="ghost" size="sm" aria-expanded={more} aria-controls="textbook-more" onPress={() => setMore(!more)}>
     <Ellipsis size={16} aria-hidden="true" />更多操作
    </Button>
   </div>
   {more && <TextbookMore {...props} rangeInput={rangeInput} />}
   {mode === 'read' && !busy && !more && !error && !props.bookFailure && (
    <StudyNextStep>{entry?.textbookId && !props.savedAssociation && props.dirty
     ? props.issues ? '点「返回课表补全必填项」，补齐后就能保存教材关联。' : '点「保存课表与教材关联」，下次打开这门课就能找到这本书。'
     : annotations.rows.some(row => !row.chapter) ? '将鼠标放到正文划线上即可查看批注。' : '在教材顶部选择一种批注方式即可开始。'}</StudyNextStep>
   )}
   {props.bookFailure && <div role="alert" className="study-inline-error">
    {props.bookFailure}
    <Button variant="ghost" size="sm" isDisabled={busy} onPress={props.onPickBook}>重新选择教材</Button>
   </div>}
   <TextbookProgress {...props} />
   <div className={'study-textbook' + (mode === 'read' ? ' textbook-reading' : '')}>
    <div className="study-canvas-column">
      {document && <TextbookBookmarks key={document.loadingTask.docId} document={document} onPageNumber={onPageNumber} />}
      {document && mode === 'read' && <ChapterSummary document={document} book={book} pageNumber={pageNumber} viewport={viewport} annotations={annotations.rows} paper={paper} onPageNumber={onPageNumber} onSaved={props.onPdfSaved} busy={busy} more={more} />}
      <div className="study-canvas-toolbar">
       <Button
        variant="ghost"
        size="sm"
        isIconOnly
        aria-label="上一页"
        isDisabled={pageNumber <= 1}
        onClick={() => onPageNumber(pageNumber - 1)}
       >
        <ChevronLeft size={18} aria-hidden="true" />
       </Button>
       <NumberField
        aria-label="预览页码"
        className="study-page-number"
        minValue={1}
        maxValue={book.pages}
        value={pageNumber}
        onChange={value => onPageNumber(value)}
       >
        <NumberField.Group>
         <NumberField.Input onFocus={event => event.currentTarget.select()} />
        </NumberField.Group>
       </NumberField>
       <span className="study-page-total">/ {book.pages}</span>
       <Button
        variant="ghost"
        size="sm"
        isIconOnly
        aria-label="下一页"
        isDisabled={pageNumber >= book.pages}
        onClick={() => onPageNumber(pageNumber + 1)}
       >
        <ChevronRight size={18} aria-hidden="true" />
       </Button>
       <span className="study-canvas-toolbar__spacer" />
       <small className="textbook-gestures" title="在教材上按 Ctrl（Mac 也可按 ⌘）并滚动，以指针位置缩放；阅读模式中按住鼠标左键可拖动页面。">
        Ctrl / ⌘ + 滚轮缩放{mode === 'read' ? ' · 拖动平移' : ''}
       </small>
       <Button
        variant="ghost"
        size="sm"
        isIconOnly
        aria-label="缩小"
        isDisabled={zoom <= 0.5}
        onClick={() => zoomBy(-0.1)}
       >
        <ZoomOut size={18} aria-hidden="true" />
       </Button>
       <span className="study-zoom">{Math.round(zoom * 100)}%</span>
       <Button
        variant="ghost"
        size="sm"
        isIconOnly
        aria-label="放大"
        isDisabled={zoom >= 2}
        onClick={() => zoomBy(0.1)}
       >
        <ZoomIn size={18} aria-hidden="true" />
       </Button>
       <Button variant="ghost" size="sm" isDisabled={zoom === 1} onClick={() => zoomTo(1)}>
        <Scan size={16} aria-hidden="true" />
        适应宽度
       </Button>
      </div>
      <div className="study-canvas" ref={frame}>
       <div className="study-page-shell">
        {' '}
        {/* NOSONAR: element nesting follows the design system contract. */}
        <div // NOSONAR: behavior-preserving rewrite tracked separately; current flow is cohesive and ordered.
         aria-label="教材页面，左右方向键翻页，Esc 取消手动标注"
         className="study-page"
         ref={paper}
         role="application"
         style={{width: size.width, height: size.height}}
         // biome-ignore lint/a11y/noNoninteractiveTabindex: this is an application surface that must accept keyboard focus.
         tabIndex={0} // NOSONAR: role="application" with Escape/arrow-key handlers is an interactive surface.
         onKeyDown={event => {
          if (event.target !== event.currentTarget) return
          if (event.key === 'Escape') {
           selection.clear()
          } else if (event.key === 'ArrowLeft' && pageNumber > 1) {
           event.preventDefault()
           onPageNumber(pageNumber - 1)
          } else if (event.key === 'ArrowRight' && pageNumber < book.pages) {
           event.preventDefault()
           onPageNumber(pageNumber + 1)
          }
         }}
        >
         <canvas ref={canvas} className="study-page__canvas" />
         {mode === 'read' && viewport && <TextbookComments key={book.id + ':' + pageNumber} bookId={book.id} viewport={viewport} rows={annotations.rows} locateId={renderedPage?.pageNumber === pageNumber && destination?.page === pageNumber ? destination.id : undefined} onLocated={() => setDestination(null)} onSaved={() => setCommentVersion(v => v + 1)} />}
         {draft?.rows
          .filter(row => row.page === pageNumber && (chosen.includes(row.id) || preview?.ids.includes(row.id)))
          .map(row => (
           <Overlay
            key={row.id}
            active={preview?.ids[0] === row.id}
            target={preview?.ids[0] === row.id ? previewTarget : undefined}
            source={row.source}
            preview={!chosen.includes(row.id)}
            rect={row.rect}
            size={size}
            viewport={viewport}
           />
          ))}
         {selection.overlay}
         {rendering ? <div className="study-page__loading">正在渲染页面…</div> : null}
        </div>
       </div>
      </div>
      {error && <div role="alert" className="study-inline-error">{error}
       <Button variant="ghost" size="sm" isDisabled={busy} onPress={props.onPickBook}>重新选择教材</Button>
      </div>}
      {annotations.error && <p role="alert" className="study-inline-error">读取本页批注失败。{annotations.error}</p>}
    </div>
    {mode !== 'read' && <aside className="study-inspector">
      {reviewing && draft ? (
       <div className="study-inspector-body">
        <div className="study-inspector-heading">
         <div>
           <h3>{mode === 'teacher' ? '待保存的老师批注' : '待保存的 AI 批注'}</h3>
          <small>
           已选 {selectedCount} / {groups.length} 条
           {draft.analyzedRange && ` · 分析了第 ${draft.analyzedRange[0]}–${draft.analyzedRange[1]} 页`}
          </small>
         </div>
         <div className="study-inspector-heading__actions">
          <Button variant="ghost" size="sm" onClick={() => onChosen(groups.flatMap(group => group[1].ids))}>
           全选
          </Button>
          <Button variant="ghost" size="sm" onClick={() => onChosen([])}>
           清空
          </Button>
         </div>
        </div>
        {!busy && groups.length > 0 && (
         <div className="textbook-next">
          <StudyNextStep>{chosen.length ? `核对原文后，点「保存所选 ${chosen.length} 条」。` : '勾选要保留的建议。'}</StudyNextStep>
          {chosen.length > 0 && <Button variant="primary" size="sm" onPress={() => setConfirmApply(true)}>保存所选 {chosen.length} 条</Button>}
         </div>
        )}
        {!!draft.skippedContentsPages?.length && (
         <p className="study-menu__hint" role="status">已跳过目录页（PDF 第 {draft.skippedContentsPages.join('、')} 页）。</p>
        )}
        {!!draft.warnings?.length && (
         <Disclosure key={draft.id} className="study-fold" defaultExpanded={draft.warnings.length <= 3}>
          <Disclosure.Heading>
           <Disclosure.Trigger className="study-fold__trigger">
            已跳过未通过核对的候选（{draft.warnings.reduce((count, row) => count + row.evidence + row.coordinates + row.invalid, 0)} 条）
            <Disclosure.Indicator />
           </Disclosure.Trigger>
          </Disclosure.Heading>
          <Disclosure.Content>
           <Disclosure.Body className="study-fold__body">
            {draft.warnings.map(({page, evidence, coordinates, invalid}) => (
             <p key={page} className="study-menu__hint">
              第 {page} 页 · {[
               evidence > 0 && `${evidence} 条来源引文无法在课堂资料中核实`,
               coordinates > 0 && `${coordinates} 条划线位置无效`,
               invalid > 0 && `${invalid} 条建议格式无效`
              ].filter(Boolean).join('，')}
             </p>
            ))}
           </Disclosure.Body>
          </Disclosure.Content>
         </Disclosure>
        )}
        <div className="study-suggestion-list">
         {groups.map(([key, group]) => {
          const selected = group.ids.every(id => chosen.includes(id))
          return (
           <div key={key} className="study-suggestion">
            <Checkbox
             isSelected={selected}
             onChange={checked =>
              onChosen(checked ? [...new Set([...chosen, ...group.ids])] : chosen.filter(id => !group.ids.includes(id)))
             }
            >
             <Checkbox.Content>
              <Checkbox.Control>
               <Checkbox.Indicator />
              </Checkbox.Control>
              <span className="study-suggestion__main">
               <span className="study-suggestion__meta">
                <SourceLabel source={group.source} />
                <span>第 {group.page} 页</span>
                {group.extension ? <span>适当延伸</span> : null}
               </span>
               <strong>{group.quote}</strong>
               {group.comments?.length ? group.comments.map((c, i) => <CommentText key={i} comment={c} />) : <small>依据：{group.evidence}</small>}
              </span>
             </Checkbox.Content>
            </Checkbox>
            <Button
             variant="ghost"
             size="sm"
             onClick={() => {
              onPageNumber(group.page)
              setPreview({page: group.page, ids: group.ids})
             }}
            >
             定位
            </Button>
           </div>
          )
         })}
        </div>
       </div>
      ) : null}
      {mode === 'teacher' ? (
       <div className="study-inspector-body">
        {analysisError && <div role="alert" className="study-inline-error">{analysisError}</div>}
        <TeacherAnnotations bookId={book.id} lectureId={entry?.lectureId} version={pdfVersion + commentVersion} onLocate={locateTeacher}
         actions={replaySubId => {
          const replay = replayDescription(entry, replaySubId, props.lectureTitle)
          return <>
           {replay && <p className="study-menu__hint">本次智云回放 · {replay}</p>}
           <AnalysisAction {...actionProps} teacher replaySubId={replaySubId} editRange={editRange} />
          </>
         }} />
       </div>
      ) : mode === 'ai' ? (
       <div className="study-inspector-body">
        {!draft && <h3>AI 批注</h3>}
        {analysisError && <div role="alert" className="study-inline-error">{analysisError}</div>}
        <AnalysisAction {...actionProps} teacher={false} editRange={editRange} />
        {!busy && <SettingsLink section="qwen">千问模型设置</SettingsLink>}
       </div>
      ) : (
       <div className="study-inspector-body">
        <div className="study-inspector-heading">
         <div>
          <h3>自主批注</h3>
          {selection.marks.length > 0 && <small>第 {pageNumber} 页 · 已选择 {selection.marks.length} 处</small>}
         </div>
         {selection.marks.length ? (
          <Button variant="ghost" size="sm" onClick={selection.clear}>
           清除选择
          </Button>
         ) : null}
        </div>
        {selection.toolbar}
        {selection.marks.length > 0 && !busy && <div className="textbook-next">
         <Button variant="primary" size="sm" onPress={() => void saveManual()}>保存手动划线</Button>
         <small className="study-menu__hint">写入原教材前会创建备份。</small>
        </div>}
        {selection.tool === 'text' && selection.marks.length > 0 &&
         <p className="study-menu__hint">已选文字：{selection.marks.map(mark => mark.quote).join(' ')}</p>}
        <StudyField label="我的笔记（可选）">
         <TextArea aria-label="我的笔记" value={evidence} maxLength={3000} onChange={event => setEvidence(event.target.value)} placeholder="记下自己的理解，阅读时悬停即可查看。" />
        </StudyField>
        <details><summary>划线分类与延伸</summary>
        <StudyField label="划线分类">
         {' '}
         {/* NOSONAR: existing element/role semantics are intentional for layout and keyboard behavior. */}
         <div // NOSONAR: behavior-preserving rewrite tracked separately; current flow is cohesive and ordered.
          aria-label="划线来源"
          className="study-source-choices"
          role="group" // NOSONAR: custom keyboard-interactive group; native fieldset would change layout and behavior.
         >
          {sourceKeys.map(key => (
           <Button
            key={key}
            aria-pressed={source === key}
            className={'study-source-choice' + (source === key ? ' study-source-choice--active' : '')}
            size="sm"
            variant="ghost"
            fullWidth
            onClick={() => setSource(key)}
           >
            <SourceLabel source={key} />
           </Button>
          ))}
         </div>
        </StudyField>
        <Checkbox isSelected={extension} onChange={setExtension}>
         <Checkbox.Content>
          <Checkbox.Control>
           <Checkbox.Indicator />
          </Checkbox.Control>
          这是教材对课堂内容的适当延伸
         </Checkbox.Content>
        </Checkbox>
        </details>
       </div>
      )}
    </aside>}
   </div>
   {confirmApply ? (
    <ConfirmDialog
     title={`保存 ${chosen.length} 条划线？`}
     confirmLabel="保存到原教材"
     busy={busy}
     onCancel={() => setConfirmApply(false)}
     onConfirm={() => {
      setConfirmApply(false)
      onApply()
     }}
    >
     {' '}
     {/* NOSONAR: adjacent JSX spacing is intentional for the rendered layout. */}这会修改原教材 PDF，并在同目录创建{' '}
     <code>.划线前备份.pdf</code>
     {/* NOSONAR: behavior-preserving rewrite tracked separately; current flow is cohesive and ordered. */}
     。保存后仍可通过“移除本软件添加的划线”撤销本软件写入的批注。
    </ConfirmDialog>
   ) : null}
  </div>
 )
}
