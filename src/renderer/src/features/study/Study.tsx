import {Button, Tabs} from '@heroui/react'
import {ArrowLeft} from 'lucide-react'
import {ConfirmDialog} from '@renderer/ui/ConfirmDialog'
import {WindowPanel} from '@renderer/ui/WindowPanel'
import {errorMessage, notify} from '@renderer/ui/ToastHost'
import {useEffect, useRef, useState} from 'react'
import {
 ScheduleContent,
 ScheduleMenu,
 type ScheduleIssue,
 type ScheduleProps
} from '@renderer/features/study/SchedulePanel'
import {TextbookContent, TextbookMenu, type TextbookProps} from '@renderer/features/study/TextbookPanel'
import {useScheduleImportStatus} from '@renderer/features/study/ScheduleImportStatus'
import {StudyNextStep} from '@renderer/features/study/StudyNextStep'
import {ScheduleMatching, useScheduleMatching} from '@renderer/features/study/ScheduleMatching'
import {validTimeZone} from '@renderer/features/study/datetime'
import {sameTeachingCourse, setSessionTextbook} from '@renderer/features/study/course-textbook'
import type {AnalysisSource, Book, Draft} from '@renderer/features/study/types'
import type {Highlight, StudyEntry} from '@shared/study'
import type {Course, Detail, Progress} from '@shared/types'

const invoke = <T,>(action: string, args?: unknown) => window.assistant.invoke<T>(action, args)

const normalize = (entry: StudyEntry): StudyEntry => ({
 id: entry.id,
 courseId: entry.courseId,
 lectureId: entry.lectureId,
 textbookId: entry.textbookId,
 title: entry.title,
 chapter: entry.chapter,
 endsAt: entry.endsAt,
 weekly: entry.weekly,
 weekday: entry.weekday,
 startPeriod: entry.startPeriod,
 endPeriod: entry.endPeriod,
 location: entry.location,
 skipZhiyunMatch: entry.skipZhiyunMatch,
 zhiyunCourseId: entry.zhiyunCourseId,
 zhiyunCourseLabel: entry.zhiyunCourseLabel,
 subId: entry.subId,
 zhiyunReplayLabel: entry.zhiyunReplayLabel
})

const snapshotOf = (entries: StudyEntry[]) => JSON.stringify(entries.map(normalize))

function analyzeNotice(source: AnalysisSource, result: Draft, rows: Highlight[], pageCount: number) {
 if (!result.rows.length && result.skippedContentsPages?.length === pageCount)
  return '已跳过目录。请翻到正文章节，或选择包含正文的页码再生成。'
 if (source === 'teacher' && !rows.length) return '本次智云回放的语音转写与所选正文页没有匹配的原话。'
 if (result.warnings?.length) return '部分候选未通过核对，已按页列出原因。你可以继续核对并保存其余建议。'
 if (result.rows.length) return '请核对划线内容、位置与依据，再保存到原教材。'
 return '此范围没有找到依据充分的内容，原教材未修改。'
}

// biome-ignore format: NOSONAR must stay on the function line
function collectIssues(entries: StudyEntry[]): ScheduleIssue[] { // NOSONAR: transitional TS fallback/UI flow retained for behavior compatibility; decomposition tracked separately to avoid sequencing regressions.
 const issues: ScheduleIssue[] = []
 for (const entry of entries) {
  if (!entry.courseId && (!entry.weekly || entry.lectureId)) issues.push({id: entry.id, message: '选择课程', field: '课程'})
  if (!entry.title.trim()) issues.push({id: entry.id, message: '填写课次名称', field: entry.weekly ? '课程名称' : '课次名称'})
  if (!entry.weekly && !entry.chapter.trim()) issues.push({id: entry.id, message: '填写教材章节', field: '教材章节'})
  if (entry.startPeriod !== undefined && entry.endPeriod !== undefined && entry.endPeriod < entry.startPeriod)
   issues.push({id: entry.id, message: '结束节次不能早于开始节次', field: '结束节次'})
  if (entry.endsAt && !Number.isFinite(Date.parse(entry.endsAt)))
   issues.push({id: entry.id, message: '填写有效的结束时间，或勾选时间待定', field: '课程结束时间'})
  if (entry.zhiyunCourseId && !/^\d+$/.test(entry.zhiyunCourseId))
   issues.push({id: entry.id, message: '智云课程编号只能是数字', field: '智云'})
  if (entry.subId && !/^\d+$/.test(entry.subId)) issues.push({id: entry.id, message: '智云课次编号只能是数字', field: '智云'})
 }
 return issues
}

// biome-ignore format: NOSONAR must stay on the function line
export function Study({ // NOSONAR: transitional TS fallback/UI flow retained for behavior compatibility; decomposition tracked separately to avoid sequencing regressions.
 visible,
 courses,
 close,
 refresh,
 openZhiyun
}: Readonly<{
 visible: boolean
 courses: Course[]
 close: () => void
 refresh?: () => Promise<unknown>
 openZhiyun: () => void
}>) {
 const [entries, setEntries] = useState<StudyEntry[]>([]),
  [initialSchedule, setInitialSchedule] = useState<'loading' | 'ready' | {error: string}>('loading'),
  [savedEntries, setSavedEntries] = useState<StudyEntry[]>([]),
  [selected, setSelected] = useState(''),
  [detail, setDetail] = useState<Detail | null>(null),
  [tab, setTab] = useState<'schedule' | 'textbook'>('schedule'),
  [timezone, setTimezone] = useState('Asia/Shanghai'),
  [books, setBooks] = useState<Record<string, Book>>({}),
  [standaloneBook, setStandaloneBook] = useState(''),
  [bookFailure, setBookFailure] = useState<{id: string; message: string} | null>(null),
  [linkTarget, setLinkTarget] = useState(''),
  [pageNumber, setPageNumber] = useState(1),
  [customRange, setCustomRange] = useState<{first: number; last: number} | null>(null),
  [draft, setDraft] = useState<Draft | null>(null),
  [analysisError, setAnalysisError] = useState(''),
  [analysisSource, setAnalysisSource] = useState<AnalysisSource>('ai'),
  [chosen, setChosen] = useState<string[]>([]),
  [reopenVersion, setReopenVersion] = useState(0),
  [pdfVersion, setPdfVersion] = useState(0),
  [busy, setBusy] = useState(false),
  [progress, setProgress] = useState<Progress | null>(null),
  [confirmClose, setConfirmClose] = useState(false)
 const working = useRef(false),
  loaded = useRef(false)
 const tracker = useScheduleImportStatus()
 const matching = useScheduleMatching(entries, setEntries, courses)
 const entry = entries.find(item => item.id === selected),
  bookId = (entry ? entry.textbookId : standaloneBook) || '',
  book = books[bookId] ?? null,
  first = customRange?.first ?? pageNumber,
  last = customRange?.last ?? Math.min(book?.pages ?? pageNumber, pageNumber + 4),
  savedEntry = savedEntries.find(item => item.id === selected),
  lectureSaved = !!entry && !!savedEntry && savedEntry.lectureId === entry.lectureId &&
   savedEntry.subId === entry.subId && savedEntry.zhiyunCourseId === entry.zhiyunCourseId &&
   savedEntry.courseId === entry.courseId && savedEntry.chapter === entry.chapter,
  issues = collectIssues(entries),
  dirty = snapshotOf(entries) !== snapshotOf(savedEntries),
  lectureTitle = detail?.lectures.find(lecture => lecture.id === entry?.lectureId)?.title,
  courseName = courses.find(course => course.id === entry?.courseId)?.name || ''
 const readingContext = useRef({selected, bookId, subId: entry?.subId, lectureId: entry?.lectureId})
 readingContext.current = {selected, bookId, subId: entry?.subId, lectureId: entry?.lectureId}
 const relatedBooks = new Set(entries.filter(row => entry && sameTeachingCourse(entry, row) && row.textbookId).map(row => row.textbookId!))
 const savedCourseBook = relatedBooks.size === 1 ? [...relatedBooks][0] : undefined
 const suggestedBook = !bookId ? savedCourseBook || standaloneBook : undefined
 const savedBookSessions = new Set(savedEntries.filter(row => row.textbookId === bookId).map(row => row.id))
 const bookSessions = entries.filter(row => row.textbookId === bookId)
 const savedAssociation = bookSessions.length === savedBookSessions.size && bookSessions.every(row => savedBookSessions.has(row.id))
 useEffect(() => {
  setAnalysisError('')
 }, [selected, bookId, reopenVersion, first, last, entry?.lectureId, entry?.subId, entry?.chapter])
 useEffect(() => {
  setDraft(current => current?.source === 'teacher' ? null : current)
 }, [entry?.lectureId, entry?.subId])
 useEffect(() => {
  setDraft(null)
  setChosen([])
 }, [selected, bookId, reopenVersion])
 useEffect(() => {
  setPageNumber(1)
  setCustomRange(null)
 }, [bookId, reopenVersion])
 useEffect(() => {
  if (!visible || tab !== 'textbook' || !bookId || book) return
  let active = true
  setBookFailure(null)
  void invoke<Book>('textbook:resolve', {id: bookId}).then(result => {
   if (active) setBooks(current => ({...current, [result.id]: result}))
  }).catch(reason => {
   if (active) setBookFailure({id: bookId, message: reason instanceof Error ? reason.message : String(reason)})
  })
  return () => { active = false }
 }, [bookId, book, visible, tab])
 async function run(task: () => Promise<void>, onError?: (reason: unknown) => void) {
  if (working.current) return false
  working.current = true
  setBusy(true)
  setProgress(null)
  try {
   await task()
   return true
  } catch (reason) {
   onError?.(reason)
   notify.error(reason)
   return false
  } finally {
   working.current = false
   setBusy(false)
   setProgress(null)
  }
 }
 function loadSchedule() {
  if (loaded.current) return
  loaded.current = true
  setInitialSchedule('loading')
  void invoke<StudyEntry[]>('study:get')
   .then(rows => {
    setEntries(rows)
    // Keep an independently opened book if the user retries after a failed timetable load.
    if (!readingContext.current.bookId) setSelected(rows[0]?.id || '')
    setSavedEntries(rows)
    setInitialSchedule('ready')
   })
   .catch(reason => {
    loaded.current = false
    setInitialSchedule({error: errorMessage(reason)})
   })
 }
 useEffect(() => {
  if (visible) loadSchedule()
 }, [visible])
 useEffect(
  () =>
   window.assistant.onEvent(event => {
    if (working.current && event.kind === 'progress') setProgress(event.data)
   }),
  []
 )
 useEffect(() => {
  let active = true
  setDetail(null)
  if (entry?.courseId)
   void invoke<Detail>('course:detail', {id: entry.courseId})
    .then(value => {
     if (active) setDetail(value)
    })
   .catch(reason => {
    if (active) notify.error(reason, {title: '读取课程信息失败'})
   })
  return () => {
   active = false
  }
 }, [entry?.courseId])
 function selectEntry(id: string) {
  if (id !== selected && draft) {
   setDraft(null)
   setChosen([])
  }
  setSelected(id)
 }
 function update(value: Partial<StudyEntry>) {
  const current = entries.find(item => item.id === selected)
  if (!current) return
  setEntries(rows => rows.map(row => (row.id === selected ? {...row, ...value} : row)))
  const affectsDraft =
   ('courseId' in value && value.courseId !== current.courseId) ||
   ('chapter' in value && value.chapter !== current.chapter) ||
   ('lectureId' in value && value.lectureId !== current.lectureId)
  if (affectsDraft && draft) {
   setDraft(null)
   setChosen([])
   notify.info('课表信息已修改，当前划线建议已清空')
  }
 }
 function addEntry(weekly = false, textbookId?: string) {
  if (!weekly && !courses.length) {
   notify.warning('还没有课程，请先创建课程')
   return
  }
  const row: StudyEntry = {
   id: crypto.randomUUID(),
   courseId: weekly ? '' : courses[0].id,
   ...(weekly ? {weekly: true} : {}),
   ...(textbookId ? {textbookId} : {}),
   title: '',
   chapter: '',
   endsAt: ''
  }
  setEntries(rows => [...rows, row])
  setSelected(row.id)
  setTab('schedule')
  return row.id
 }
 function deleteEntry() {
  if (!entry) return
  const index = entries.findIndex(item => item.id === entry.id),
   rows = entries.filter(item => item.id !== entry.id)
  setEntries(rows)
  setSelected(rows[Math.min(index, rows.length - 1)]?.id || '')
  setDraft(null)
  setChosen([])
 }
 function clearSchedule() {
  matching.reset()
  tracker.clear()
  setEntries([])
  setSelected('')
  setDraft(null)
  setChosen([])
 }
 function save() {
  void run(async () => {
   matching.stop()
   const rows = await invoke<StudyEntry[]>('study:save', {entries})
   // Keep any edits made while the save was in flight as a new draft.
   setEntries(current => current === entries ? rows : current)
   setSavedEntries(rows)
   tracker.markSaved()
   notify.success(`课表已保存，共 ${rows.length} 个课次。`)
   if (refresh) {
    try {
     await refresh()
    } catch (reason) {
     notify.error(reason, {title: '课表已保存，刷新本地课程详情失败'})
    }
   }
  })
 }
 async function parseSchedule(text: string | null, nextTimezone: string) {
  const fromImage = text === null
  if (!fromImage && !validTimeZone(nextTimezone)) {
   notify.warning('课表时区无效，请填写类似 Asia/Shanghai 的时区。')
   return false
  }
  let imported = false, requestId: string | undefined
  await run(async () => {
   if (fromImage) requestId = tracker.begin()
   const input = fromImage ? {source: 'image', requestId} : {text, timezone: nextTimezone}
   const rows = await invoke<StudyEntry[] | null>('study:parse', input)
   if (!rows) {
    if (requestId) tracker.cancel(requestId)
    return
   }
   if (!rows.length) throw new Error(fromImage ? '图片中没有识别到课程，请换一张清晰的课表截图或手动添加。' : '课表缺少明确的日期、结束时间或章节，或课程无法匹配，请补充后重试')
   if (!fromImage) {
    matching.reset()
    tracker.clear()
    setTimezone(nextTimezone)
   }
   setEntries(current => fromImage ? [...current, ...rows] : rows)
   setSelected(rows[0].id)
   setDraft(null)
   setChosen([])
   imported = true
   if (requestId) {
    tracker.finish(requestId, rows.length)
    void matching.match(rows)
   } else notify.info(`已识别 ${rows.length} 条课次，请核对后保存。`, {timeout: 6000})
  }, reason => {
   if (requestId) tracker.fail(requestId, reason)
  })
  return imported
 }
 function pickBook() {
  const target = selected
  const previousBookId = bookId
  void run(async () => {
   const result = await invoke<Book | null>('textbook:pick')
   if (!result) return
   // Reopening a path keeps its catalog ID even if the PDF bytes have changed.
   if (readingContext.current.bookId === result.id) setReopenVersion(value => value + 1)
   setBooks(current => ({...current, [result.id]: result}))
   setBookFailure(null)
   if (target) setEntries(rows => setSessionTextbook(rows, target, result.id, true))
   else setStandaloneBook(result.id)
   if (readingContext.current.selected === target) setTab('textbook')
  }, reason => {
   if (readingContext.current.selected === target && readingContext.current.bookId === previousBookId)
    setBookFailure({id: previousBookId, message: errorMessage(reason)})
  })
 }
 function changeFirst(value: number) {
  setCustomRange(range => ({first: value, last: range?.last ?? last}))
  if (draft) {
   setDraft(null)
   setChosen([])
  }
 }
 function changeLast(value: number) {
  setCustomRange(range => ({first: range?.first ?? first, last: value}))
  if (draft) {
   setDraft(null)
   setChosen([])
  }
 }
 function goPage(value: number) {
  if (!book || !Number.isFinite(value)) return
  setPageNumber(Math.max(1, Math.min(book.pages, Math.trunc(value))))
 }
 function analyze(source: AnalysisSource) {
  const hasClass = source !== 'ai' && !!(entry?.subId || entry?.lectureId)
  const currentSource = () => {
   const current = readingContext.current
   return current.selected === selected && current.bookId === book?.id &&
    (!hasClass || (current.subId === entry?.subId && current.lectureId === entry?.lectureId))
  }
  if (hasClass && !lectureSaved) {
   notify.warning('请先保存课表中的课堂资料关联，再生成划线建议。')
   return
  }
  if (!book) {
   notify.warning('请先选择教材 PDF。')
   return
  }
  if (first < 1 || last < first || last > book.pages || last - first > 99) {
   notify.warning('请选择不超过 100 页的有效页码范围。')
   return
  }
  void run(async () => {
   setAnalysisSource(source)
   setAnalysisError('')
   setDraft(null)
   setChosen([])
   const result = await invoke<Draft>('textbook:analyze', {id: book.id, entryId: hasClass ? entry?.id : undefined, first, last})
   if (!currentSource()) return
   // A textbook-only explanation can share a draft with a transcript match.
   // The teacher review and its save button operate only on marks with an actual transcript quote.
   const rows = source === 'teacher' ? result.rows.filter(row => row.comments?.some(c => c.kind === 'teacher' && c.quote?.trim())) : result.rows
   setDraft({...result, rows, source, analyzedRange: [first, last]})
   setChosen(rows.map(row => row.id))
   notify.info(analyzeNotice(source, result, rows, last - first + 1), {timeout: 6000})
  }, reason => {
   if (currentSource()) setAnalysisError(errorMessage(reason))
  })
 }
 function applyDraft() {
  if (!draft || !chosen.length) return
  void run(async () => {
   const result = await invoke<{path: string; count: number}>('textbook:apply', {id: draft.id, ids: chosen})
   setDraft(null)
   setChosen([])
   setPdfVersion(value => value + 1)
   notify.success(`已保存 ${result.count} 条划线到原教材；备份：${result.path}.划线前备份.pdf`, {
    timeout: 10000
   })
  })
 }
 async function manual(value: {
  marks: {rect: Highlight['rect']; quote: string; ink?: [number, number][]}[]
  source: Highlight['source']
  evidence: string
  extension: boolean
 }) {
  if (!book || !value.marks.length) return false
  return run(async () => {
   await invoke('textbook:manual', {
    id: book.id,
    highlights: value.marks.map(mark => ({
     id: crypto.randomUUID(),
     page: pageNumber,
     ...mark,
     source: value.source,
     evidence: value.evidence.trim() || '手动标注',
     comments: value.evidence.trim() ? [{kind: 'personal', text: value.evidence.trim()}] : [],
     extension: value.extension
    }))
   })
   setPdfVersion(current => current + 1)
   notify.success('手动划线已保存到原教材。')
  })
 }
 function removeHighlights() {
  if (!book) return
  void run(async () => {
   await invoke('textbook:clear', {id: book.id})
   setDraft(null)
   setChosen([])
   setPdfVersion(current => current + 1)
   notify.success('已移除本软件添加的划线，原有批注保留。')
  })
 }
 const scheduleProps: ScheduleProps = {
  courses,
  entries,
  selected,
  detail,
  timezone,
  busy,
  dirty,
  issues,
  onSelect: selectEntry,
  onAdd: addEntry,
  onUpdate: update,
  onDelete: deleteEntry,
  onClear: clearSchedule,
  onSave: save,
  onCourseBook: () => entry?.textbookId ? setTab('textbook') : pickBook(),
  linkTarget,
  onLinkShown: () => setLinkTarget(''),
  onTimezone: setTimezone,
  importStatus: tracker.status,
  onParse: parseSchedule
 }
 const textbookProps: TextbookProps = {
  entry,
  analysisError,
  analysisSource,
  entries,
  onSelectEntry: id => {
   if (!id && bookId) setStandaloneBook(bookId)
   selectEntry(id)
  },
  bookFailure: bookFailure?.id === bookId ? bookFailure.message : '',
  resolvingBook: !!bookId && !book && bookFailure?.id !== bookId,
  savedAssociation,
  lectureSaved,
  dirty,
  issues: issues.length,
  onReviewIssues: () => {
   if (issues[0]) selectEntry(issues[0].id)
   setTab('schedule')
  },
  onSaveSchedule: save,
  bookSuggestion: savedCourseBook ? '使用本课程已选教材' : '将刚才单独阅读的教材用于此课程',
  useCourseBook: entry && suggestedBook ? () => setEntries(rows => setSessionTextbook(rows, entry.id, suggestedBook, true)) : undefined,
  timezone,
  onLinkSession: (id, checked) => {
   if (!bookId) return
   const previousBook = savedEntries.find(row => row.id === id)?.textbookId
   setEntries(rows => setSessionTextbook(rows, id, bookId, checked, previousBook))
   if (!checked && id === selected) {
    // Keep the open PDF visible when its current session is unchecked.
    setStandaloneBook(bookId)
    selectEntry('')
   }
  },
  lectureTitle,
  book,
  pageNumber,
  first,
  last,
  followsPage: customRange === null,
  onFollowPage: () => {
   setCustomRange(null)
   setDraft(null)
   setChosen([])
  },
  draft,
  chosen,
  busy,
  progress,
  pdfVersion,
  onPdfSaved: () => setPdfVersion(value => value + 1),
  onPickBook: pickBook,
  onAnalyze: analyze,
  onOpenBook: () => {
   if (book) void run(() => invoke('textbook:open', {id: book.id}))
  },
  onRemoveHighlights: removeHighlights,
  onCancel: () => void invoke('cancel'),
  onPageNumber: goPage,
  onFirst: changeFirst,
  onLast: changeLast,
  onChosen: setChosen,
  onApply: applyDraft,
  onManual: manual,
  onGoSchedule: () => {
   // Standalone readers can start an unsaved course association with this PDF already attached.
   setLinkTarget(entry?.id || addEntry(true, book?.id) || '')
   setTab('schedule')
  }
 }
 function scheduleBody() {
  if (initialSchedule === 'loading')
   return <output className="study-menu__hint">正在读取已保存的课表…</output>
  if (typeof initialSchedule === 'object')
   return (
    <div className="textbook-next">
     <p role="alert" className="study-inline-error">
      读取课表失败。{initialSchedule.error}
     </p>
     <StudyNextStep>点「重新读取课表」，载入已保存的课程。</StudyNextStep>
     <Button variant="primary" size="sm" onPress={loadSchedule}>
      重新读取课表
     </Button>
    </div>
   )
  return (
   <ScheduleContent {...scheduleProps}>
    {toolsOpen => (
     <ScheduleMatching
      matching={matching}
      entries={entries}
      selected={selected}
      onSelect={selectEntry}
      courses={courses}
      busy={busy}
      openZhiyun={openZhiyun}
      toolsOpen={toolsOpen}
     />
    )}
   </ScheduleContent>
  )
 }
 const welcome = !entries.length && (tab === 'schedule' || !book)
 const activeCourse = tab === 'textbook' && entry
  ? [entry.title || '未命名课次', courseName !== entry.title ? courseName : ''].filter(Boolean).join(' · ')
  : undefined
 function closeStudy() {
  if (dirty) setConfirmClose(true)
  else close()
 }
 return (
  <>
   <WindowPanel
    visible={visible}
    className={'study-window' + (tab === 'textbook' ? ' study-window--textbook' : '') + (welcome ? ' study-window--welcome' : '')}
    label="课表与教材"
    title={tab === 'schedule' ? '课次列表' : '我的教材'}
    description={null}
    heading="课表与教材"
    subheading={activeCourse}
    close={closeStudy}
    actions={welcome && (
     <Button variant="ghost" size="sm" onPress={closeStudy}><ArrowLeft size={16} aria-hidden="true" />返回课程对话</Button>
    )}
    menu={tab === 'schedule' ? <ScheduleMenu {...scheduleProps} /> : <TextbookMenu {...textbookProps} />}
   >
    <Tabs
     className="study-tabs"
     selectedKey={tab}
     onSelectionChange={key => {
      if (key === 'schedule' || key === 'textbook') setTab(key)
     }}
    >
     <div className="study-modebar">
      <div className="app-tabs__list-container tabs__list-container">
       <Tabs.List aria-label="课表与教材">
        <Tabs.Tab className="app-tabs__tab" id="schedule">
         课表
         <Tabs.Indicator />
        </Tabs.Tab>
        <Tabs.Tab className="app-tabs__tab" id="textbook">
         教材划线
         <Tabs.Indicator />
        </Tabs.Tab>
       </Tabs.List>
      </div>
     </div>
     <Tabs.Panel
      className={'study-panel' + (tab !== 'schedule' ? ' study-panel--hidden' : '')}
      id="schedule"
      shouldForceMount
     >
      {scheduleBody()}
     </Tabs.Panel>
     <Tabs.Panel
      className={'study-panel' + (tab !== 'textbook' ? ' study-panel--hidden' : '')}
      id="textbook"
      shouldForceMount
     >
      {initialSchedule === 'loading' ? (
       <output className="study-menu__hint">正在读取教材的课程关联…</output>
      ) : (
       <TextbookContent key={`${bookId}/${reopenVersion}`} {...textbookProps} />
      )}
     </Tabs.Panel>
    </Tabs>
   </WindowPanel>
   {confirmClose ? (
    <ConfirmDialog
     title="放弃未保存的课表修改？"
     confirmLabel="放弃并关闭"
     onCancel={() => setConfirmClose(false)}
     onConfirm={() => {
      matching.reset()
      tracker.clear()
      setEntries(savedEntries)
      setSelected(savedEntries.find(row => row.id === selected)?.id ?? savedEntries[0]?.id ?? '')
      setDraft(null)
      setChosen([])
      setConfirmClose(false)
      close()
     }}
    >
     课表还有未保存的修改。放弃后将恢复为上次保存的课表。
    </ConfirmDialog>
   ) : null}
  </>
 )
}
