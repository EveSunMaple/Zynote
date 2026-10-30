import {Button, Input} from '@heroui/react'
import {useEffect, useMemo, useRef, useState} from 'react'
import type {ZhiyunCourse, ZhiyunLesson, ZhiyunTask} from '@shared/types'
import {WindowMenuButton, WindowPanel} from '@renderer/ui/WindowPanel'
import {SelectField} from '@renderer/ui/SelectField'
import {errorMessage, notify} from '@renderer/ui/ToastHost'
import {useSettingsNavigation} from '@renderer/app/settings-navigation'
const invoke = <T,>(action: string, args?: unknown) => window.assistant.invoke<T>(action, args)
const courseLabel = (course: ZhiyunCourse) => [course.title, course.teacher, course.term].filter(Boolean).join(' · ')
const courseKey = (course: Pick<ZhiyunCourse, 'title' | 'teacher'>) => course.title + '\u0000' + course.teacher
const lessonCacheTtl = 2 * 60 * 1000
const maxSearchPages = 12
const activeTaskStates = new Set<ZhiyunTask['state']>(['queued', 'running', 'cancelling'])
export function ZhiyunImporter({
 visible,
 close,
 localCourseId,
 imported
}: Readonly<{
 visible: boolean
 close: () => void
 localCourseId: string
 imported: (lectureId: string, courseId: string) => Promise<void>
}>) {
 const settings = useSettingsNavigation()
 const [keyword, setKeyword] = useState(''),
  [courses, setCourses] = useState<ZhiyunCourse[]>([]),
  [course, setCourse] = useState<ZhiyunCourse | null>(null),
  [lessons, setLessons] = useState<ZhiyunLesson[]>([]),
  [subId, setSubId] = useState('')
 const [searching, setSearching] = useState(false),
  [loadingAll, setLoadingAll] = useState(false),
  [searchProgress, setSearchProgress] = useState(''),
  [lessonsLoading, setLessonsLoading] = useState(false),
  [progress, setProgress] = useState(''),
  [saved, setSaved] = useState(''),
  [page, setPage] = useState(1),
  [hasMore, setHasMore] = useState(false),
  [searchedKeyword, setSearchedKeyword] = useState(''),
  [task, setTask] = useState<ZhiyunTask | null>(null)
 const lessonsCache = useRef(new Map<string, {at: number; promise: Promise<ZhiyunLesson[]>}>()),
  lessonRequest = useRef(0),
  notifiedTask = useRef(''),
  searchGeneration = useRef(0),
  taskRef = useRef<ZhiyunTask | null>(null),
  importedRef = useRef(imported)
 const taskActive = Boolean(task && activeTaskStates.has(task.state))
 taskRef.current = task
 importedRef.current = imported
 async function handleTerminalTask(next: ZhiyunTask) {
  if (activeTaskStates.has(next.state) || notifiedTask.current === next.id) return
  notifiedTask.current = next.id
  if (next.state === 'completed') {
   if (next.outputPath) setSaved(next.outputPath)
   if (next.lectureId && next.resultCourseId) {
    try {
     await importedRef.current(next.lectureId, next.resultCourseId)
    } catch (e) {
     notify.error(e, {title: '刷新课程失败'})
    }
   }
   if (next.warning) notify.warning(next.warning, {description: next.outputPath, timeout: 12000})
   else notify.success('智云资源已导出并关联课次', {description: next.outputPath, timeout: 6000})
   setProgress('')
   return
  }
  if (next.state === 'failed') notify.error(next.error || '导出失败', {title: '导出智云资源失败'})
  else if (next.state === 'cancelled') notify.info('已取消导出')
  else notify.error(next.error || '任务已中断，可在任务中心重试', {title: '智云导出中断'})
  setProgress('')
 }
 useEffect(
  () =>
   window.assistant.onEvent(e => {
    if (e.kind === 'progress' && e.data.stage === '智云课堂') setProgress(e.data.message)
    if (e.kind === 'zhiyun-task' && taskRef.current && e.data.id === taskRef.current.id) {
     if (e.data.state === 'queued') notifiedTask.current = ''
     setTask(e.data)
     void handleTerminalTask(e.data)
    }
   }),
  []
 )
 useEffect(() => {
  if (!visible) return
  void invoke<ZhiyunTask[]>('zhiyun:tasks')
   .then(rows => {
    const active = rows.find(row => activeTaskStates.has(row.state))
    if (active) {
     notifiedTask.current = ''
     setTask(active)
    }
   })
   .catch(() => {})
 }, [visible])
 useEffect(() => {
  if (!task || !taskActive) return
  let disposed = false
  const poll = async () => {
   try {
    const rows = await invoke<ZhiyunTask[]>('zhiyun:tasks')
    if (disposed) return
    const next = rows.find(row => row.id === task.id)
    if (!next) return
    setTask(next)
    await handleTerminalTask(next)
   } catch (e) {
    if (!disposed) notify.error(e, {title: '读取导出任务失败', key: 'zhiyun-task-poll', dedupeWindow: 8000})
   }
  }
  void poll()
  const timer = setInterval(() => void poll(), 1000)
  return () => {
   disposed = true
   clearInterval(timer)
  }
 }, [task?.id, taskActive])
 function requestLessons(id: string) {
  const cached = lessonsCache.current.get(id)
  if (cached && Date.now() - cached.at < lessonCacheTtl) return cached.promise
  const promise = invoke<ZhiyunLesson[]>('zhiyun:lessons', {courseId: id})
  lessonsCache.current.set(id, {at: Date.now(), promise})
  promise.catch(() => {
   if (lessonsCache.current.get(id)?.promise === promise) lessonsCache.current.delete(id)
  })
  return promise
 }
 async function loadLessons(value: ZhiyunCourse): Promise<ZhiyunLesson[] | null> {
  const request = ++lessonRequest.current
  setCourse(value)
  setLessonsLoading(true)
  setProgress('')
  try {
   const rows = await requestLessons(value.id)
   if (request !== lessonRequest.current) return null
   setLessons(rows)
   setSubId(rows[0]?.id || '')
   return rows
  } catch (e) {
   if (request !== lessonRequest.current) return null
   setLessons([])
   setSubId('')
   notify.error(e, {title: '读取课次失败'})
   return null
  } finally {
   if (request === lessonRequest.current) setLessonsLoading(false)
  }
 }
 function chooseCourse(terms: ZhiyunCourse[]) {
  for (const term of terms.slice(0, 4)) void requestLessons(term.id).catch(() => {})
  void loadLessons(terms[0])
 }
 const groups = useMemo(() => {
  const map = new Map<string, {key: string; title: string; teacher: string; terms: ZhiyunCourse[]}>()
  for (const c of courses) {
   const key = courseKey(c),
    group = map.get(key)
   if (group) group.terms.push(c)
   else map.set(key, {key, title: c.title, teacher: c.teacher, terms: [c]})
  }
  for (const group of map.values()) {
   group.terms.sort((a, b) => (b.term || '').localeCompare(a.term || ''))
   group.terms = group.terms.filter(
    (t, i, all) => !t.term || t.id === course?.id || all.findIndex(x => x.term === t.term) === i
   )
  }
  return [...map.values()]
 }, [courses, course?.id])
 const activeGroup = course ? groups.find(group => group.key === courseKey(course)) : undefined
 function mergeCourses(rows: ZhiyunCourse[]) {
  setCourses(current => [...new Map([...current, ...rows].map(c => [c.id, c])).values()])
 }
 async function loadRemaining(generation: number, start: number, term: string) {
  setLoadingAll(true)
  try {
   let next = start,
    more = true
   while (more && next <= maxSearchPages) {
    setSearchProgress(`正在加载全部学期（第 ${next} 页）…`)
    const result = await invoke<{courses: ZhiyunCourse[]; hasMore: boolean}>('zhiyun:search', {
     keyword: term,
     page: next
    })
    if (generation !== searchGeneration.current) return
    mergeCourses(result.courses)
    setPage(next)
    setHasMore(result.hasMore)
    more = result.hasMore && result.courses.length > 0
    next++
   }
   if (generation !== searchGeneration.current) return
   setSearchProgress(more ? '已加载部分结果，可点击“加载更多课程”继续' : '')
  } catch (e) {
   if (generation === searchGeneration.current) {
    setSearchProgress('加载全部学期失败：' + errorMessage(e))
    notify.error(e, {title: '加载全部学期失败'})
   }
  } finally {
   if (generation === searchGeneration.current) setLoadingAll(false)
  }
 }
 async function search() {
  const generation = ++searchGeneration.current,
   term = keyword.trim()
  setSearching(true)
  setLoadingAll(false)
  setSearchProgress('')
  setSearchedKeyword(term)
  setCourse(null)
  setLessons([])
  setSubId('')
  setSaved('')
  setProgress('')
  if (!taskActive) {
   setTask(null)
   notifiedTask.current = ''
  }
  setLessonsLoading(false)
  setCourses([])
  setPage(1)
  setHasMore(false)
  lessonsCache.current.clear()
  lessonRequest.current++
  try {
   let url: URL | null = null
   try {
    url = new URL(keyword)
   } catch {}
   if (url && /(^|\.)cmc\.zju\.edu\.cn$/.test(url.hostname) && /^\d+$/.test(url.searchParams.get('course_id') || '')) {
    setSearchedKeyword('')
    const value = {
     id: url.searchParams.get('course_id')!,
     title: '智云课程 ' + url.searchParams.get('course_id'),
     teacher: '',
     term: ''
    }
    setCourses([value])
    setHasMore(false)
    const rows = await loadLessons(value)
    const sub = url.searchParams.get('sub_id')
    if (rows && sub) setSubId(sub)
    return
   }
   const result = await invoke<{courses: ZhiyunCourse[]; hasMore: boolean}>('zhiyun:search', {keyword: term, page: 1})
   if (generation !== searchGeneration.current) return
   setCourses(result.courses)
   setPage(1)
   setHasMore(result.hasMore)
   setSearching(false)
   if (!result.courses.length && !result.hasMore) setSearchProgress('没有找到课程，请换一个名称搜索')
   else if (result.hasMore) void loadRemaining(generation, 2, term)
  } catch (e) {
   if (generation === searchGeneration.current) {
    setSearchProgress('搜索失败：' + errorMessage(e))
    notify.error(e, {title: '搜索智云课程失败'})
   }
  } finally {
   if (generation === searchGeneration.current) setSearching(false)
  }
 }
 async function loadMore() {
  if (searching || loadingAll || !hasMore || !searchedKeyword) return
  setLoadingAll(true)
  try {
   const next = page + 1
   const result = await invoke<{courses: ZhiyunCourse[]; hasMore: boolean}>('zhiyun:search', {
    keyword: searchedKeyword,
    page: next
   })
   mergeCourses(result.courses)
   setPage(next)
   setHasMore(result.hasMore)
   setSearchProgress(result.hasMore ? '已加载更多课程，可继续加载' : '')
  } catch (e) {
   notify.error(e, {title: '加载更多课程失败'})
  } finally {
   setLoadingAll(false)
  }
 }
 async function exportResource(kind: 'transcript' | 'ppt') {
  if (!course || !subId || taskActive || lessonsLoading) return
  setSaved('')
  setProgress(kind === 'transcript' ? '正在准备语音识别原文…' : '正在准备 PPT…')
  try {
   const next = await invoke<ZhiyunTask>('zhiyun:export', {
    courseId: course.id,
    courseName: course.title,
    subId,
    kind,
    title: lesson?.title || '',
    localCourseId: localCourseId || undefined
   })
   notifiedTask.current = ''
   setTask(next)
   if (next.state === 'failed') notify.error(next.error || '导出失败', {title: '导出智云资源失败'})
  } catch (e) {
   notify.error(e, {title: '导出智云资源失败'})
  }
 }
 async function cancelExport() {
  if (!task || !taskActive) return
  try {
   setTask(await invoke<ZhiyunTask>('zhiyun:task-cancel', {id: task.id}))
  } catch (e) {
   notify.error(e, {title: '取消导出失败'})
  }
 }
 const lesson = lessons.find(row => row.id === subId) // NOSONAR: array lookup is intentional for small fixed lists.
 const locked = searching || taskActive
 let subheading = progress || (course ? courseLabel(course) : '登录后搜索课程，导出原文与 PPT')
 if (!course && searchProgress) subheading = searchProgress
 else if (course && searchProgress && !lessonsLoading) subheading = courseLabel(course) + ' · ' + searchProgress
 if (lessonsLoading) subheading = course ? courseLabel(course) + ' · 正在加载课次…' : '正在加载课次…'
 if (taskActive) subheading = `${task?.title || '智云导出'} · ${task?.stage || '正在导出'}`
 else if (task?.state === 'failed' && task.error) subheading = `${task.title} · ${task.error}`
 return (
  <WindowPanel
   visible={visible}
   label="智云导入"
   title="智云导入"
   description="搜索智云课程，导出语音原文与课件；登录在设置中完成。"
   heading="智云导入"
   subheading={subheading}
   menu={
    <>
     {groups.map(group => (
      <WindowMenuButton
       key={group.key}
       active={group.key === activeGroup?.key}
       disabled={locked}
       title={[group.title, group.teacher].filter(Boolean).join(' · ')}
       onSelect={() => chooseCourse(group.terms)}
      >
       {group.title}
       {group.teacher ? ` · ${group.teacher}` : ''}
      </WindowMenuButton>
     ))}
     {!groups.length && <p className="window-menu-empty">搜索后在这里选择课程</p>}
     {hasMore && !loadingAll && (
      <WindowMenuButton disabled={locked} onSelect={() => void loadMore()}>
       加载更多课程
      </WindowMenuButton>
     )}
    </>
   }
   actions={
    <Button variant="tertiary" size="sm" isDisabled={locked} onClick={() => settings.open('zhiyun')}>
     登录智云课堂
    </Button>
   }
   close={close}
  >
   <div className="panel-section">
    <form
     className="panel-inline-form"
     onSubmit={e => {
      e.preventDefault()
      if (!searching) void search()
     }}
    >
     <Input
      aria-label="搜索智云课程"
      placeholder="搜索课程名称，或粘贴智云课程链接"
      value={keyword}
      disabled={locked}
      onChange={e => setKeyword(e.target.value)}
     />
     <Button type="submit" variant="tertiary" size="sm" isDisabled={locked || !keyword.trim()}>
      搜索
     </Button>
    </form>
    <div className="row zhiyun-picks">
     {activeGroup && (
      <label>
       选择学期
       <SelectField
        label="选择智云学期"
        placeholder="选择学期"
        value={course?.id || ''}
        isDisabled={locked}
        disabledKeys={loadingAll ? ['__loading__'] : []}
        onChange={next => {
         const value = activeGroup.terms.find(t => t.id === next)
         if (value && value.id !== course?.id) void loadLessons(value)
        }}
        options={[
         ...activeGroup.terms.map(t => ({
          value: t.id,
          label: t.term || '未标学期'
         })),
         ...(loadingAll ? [{value: '__loading__', label: '正在加载其他学期…'}] : [])
        ]}
       />
      </label>
     )}
     <label>
      选择课次
      <SelectField
       label="选择智云课次"
       placeholder="选择具体课次"
       value={subId}
       isDisabled={locked || !lessons.length}
       onChange={next => setSubId(next)}
       options={[
        {value: '', label: '选择具体课次'},
        ...lessons.map(row => ({
         value: row.id,
         label: row.title + (row.teacher ? ' · ' + row.teacher : '')
        }))
       ]}
      />
     </label>
    </div>
    {course && !lessons.length && !lessonsLoading && !searching && <p className="hint">本课程暂无可读取的课次</p>}
    <div className="row">
     <Button
      variant="primary"
      size="sm"
      isDisabled={locked || !lesson}
      onClick={() => void exportResource('transcript')}
     >
      导出语音识别-原文
     </Button>
     <Button variant="tertiary" size="sm" isDisabled={locked || !lesson} onClick={() => void exportResource('ppt')}>
      导出 PPT
     </Button>
     {taskActive && (
      <Button variant="ghost" size="sm" isDisabled={task?.state === 'cancelling'} onClick={() => void cancelExport()}>
       {task?.state === 'cancelling' ? '正在取消…' : '取消导出'}
      </Button>
     )}
    </div>
    <p className="hint">原文保存到“语音原文”，课件保存到“PPT”；自动关联同一课次。导出任务可在任务中心取消或重试。</p>
    {task?.warning && <p className="hint">提示：{task.warning}</p>}
    {saved && <Input aria-label="智云资源保存路径" readOnly value={saved} />}
   </div>
  </WindowPanel>
 )
}
