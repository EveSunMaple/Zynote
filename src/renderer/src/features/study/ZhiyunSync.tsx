import {Button} from '@heroui/react'
import {useEffect, useRef, useState} from 'react'
import {dateTimeInZone} from '@renderer/features/study/datetime'
import {StudyField} from '@renderer/features/study/Field'
import {SelectField} from '@renderer/ui/SelectField'
import {errorMessage, notify} from '@renderer/ui/ToastHost'
import type {StudyEntry} from '@shared/study'
import type {Course, ZhiyunCourse, ZhiyunCourseMatch, ZhiyunLesson} from '@shared/types'

const invoke = <T,>(action: string, args?: unknown) => window.assistant.invoke<T>(action, args)

const courseLabel = (course: ZhiyunCourse) =>
 [course.title, course.teacher, course.term].filter(Boolean).join(' · ') || course.id

const lessonLabel = (lesson: ZhiyunLesson) =>
 [lesson.title, lesson.teacher, lesson.videoAvailable ? '可回放' : '暂无回放'].filter(Boolean).join(' · ') || lesson.id

function replayDate(title: string) {
 const matches = [...title.matchAll(/(?<!\d)(\d{4})[-/.年](\d{1,2})[-/.月](\d{1,2})(?!\d)/g)]
 if (matches.length !== 1) return
 const [, year, month, day] = matches[0]
 const value = [year, month.padStart(2, '0'), day.padStart(2, '0')].join('-')
 const date = new Date(Date.UTC(Number(year), Number(month) - 1, Number(day)))
 return date.toISOString().slice(0, 10) === value ? date : undefined
}

function matchesWeeklySlot(slot: StudyEntry, lesson: ZhiyunLesson) {
 if (!slot.weekly || !slot.weekday || !slot.startPeriod || !slot.endPeriod || !lesson.videoAvailable) return false
 const title = lesson.title.normalize('NFKC'),
  date = replayDate(title)
 if (!date || (date.getUTCDay() || 7) !== slot.weekday) return false
 const normalizedTitle = title.replace(/节\s*([-~～–—至到])/g, '$1').replace(/([-~～–—至到])\s*第\s*/g, '$1')
 const ranges = [...normalizedTitle.matchAll(/第\s*(\d{1,2})\s*[-~～–—至到]\s*(\d{1,2})\s*节/g)]
 const periods = ranges.length
  ? ranges
  : [...normalizedTitle.matchAll(/第\s*(\d{1,2})\s*节/g)].map(match => [match[0], match[1], undefined])
 if (periods.length !== 1) return false
 const [, start, end = start] = periods[0]
 return Number(start) === slot.startPeriod && Number(end) === slot.endPeriod
}

function entryDate(entry: StudyEntry) {
 if (!entry.endsAt) return ''
 return dateTimeInZone(entry.endsAt, 'Asia/Shanghai')?.toString().slice(0, 10) || ''
}

function autoReplay(entry: StudyEntry, lessons: ZhiyunLesson[]) {
 const playable = lessons.filter(lesson => lesson.videoAvailable)
 if (!playable.length) return undefined
 if (entry.weekly) {
  const matched = playable.filter(lesson => matchesWeeklySlot(entry, lesson))
  return matched.length === 1 ? matched[0] : undefined
 }
 const date = entryDate(entry)
 if (!date) return undefined
 const matched = playable.filter(lesson => replayDate(lesson.title)?.toISOString().slice(0, 10) === date)
 return matched.length === 1 ? matched[0] : undefined
}

function loginHint(message: string) {
 return /登录|登录状态|login/i.test(message) ? '请先在左侧「智云课堂」登录，再重试自动匹配。' : message
}

export function ZhiyunSync({
 entry,
 courses: localCourses,
 busy,
 onUpdate
}: Readonly<{
 entry: StudyEntry
 courses: Course[]
 busy: boolean
 onUpdate: (value: Partial<StudyEntry>) => void
}>) {
 const [matching, setMatching] = useState(false),
  [courses, setCourses] = useState<ZhiyunCourse[]>([]),
  [lessons, setLessons] = useState<ZhiyunLesson[] | null>(null),
  [loadingLessons, setLoadingLessons] = useState(false),
  [message, setMessage] = useState('')
 const request = useRef(0)
 useEffect(() => {
  request.current++
  setCourses([])
  setLessons(null)
  setLoadingLessons(false)
  setMatching(false)
  setMessage('')
 }, [entry.id])
 async function loadLessons(courseId: string, candidates: ZhiyunCourse[]) {
  const current = ++request.current
  setLoadingLessons(true)
  setLessons(null)
  try {
   const rows = await invoke<ZhiyunLesson[]>('zhiyun:lessons', {courseId})
   if (current !== request.current) return
   setLessons(rows)
   const replay = autoReplay(entry, rows)
   if (replay) {
    onUpdate({subId: replay.id, zhiyunReplayLabel: lessonLabel(replay)})
    setMessage('已自动选中匹配的课堂回放。')
   } else if (!rows.length) {
    setMessage('这门课暂无可选课次。')
   } else {
    setMessage('请选择课堂回放。')
   }
   const course = candidates.find(item => item.id === courseId)
   if (course) onUpdate({zhiyunCourseLabel: courseLabel(course)})
  } catch (reason) {
   if (current !== request.current) return
   const text = errorMessage(reason)
   setMessage(loginHint(text))
   notify.error(reason, {title: '读取智云回放失败'})
  } finally {
   if (current === request.current) setLoadingLessons(false)
  }
 }
 async function chooseCourse(id: string, candidates = courses) {
  const course = candidates.find(item => item.id === id)
  onUpdate({
   zhiyunCourseId: id,
   zhiyunCourseLabel: course ? courseLabel(course) : entry.zhiyunCourseLabel,
   subId: undefined,
   zhiyunReplayLabel: undefined,
   skipZhiyunMatch: undefined
  })
  await loadLessons(id, candidates)
 }
 const searchTitle = (
  entry.weekly ? entry.title : localCourses.find(course => course.id === entry.courseId)?.name || entry.title
 ).trim()
 async function match() {
  const title = searchTitle
  if (!title) {
   setMessage('请先填写课程名称，再自动匹配。')
   return
  }
  const current = ++request.current
  setMatching(true)
  setCourses([])
  setLessons(null)
  setMessage('正在匹配智云课程…')
  try {
   const result = await invoke<ZhiyunCourseMatch>('zhiyun:match-course', {title})
   if (current !== request.current) return
   setCourses(result.courses)
   const matched =
    result.status === 'matched'
     ? result.matchedId
     : result.courses.length === 1 && !result.truncated
       ? result.courses[0].id
       : undefined
   if (matched) {
    await chooseCourse(matched, result.courses)
   } else if (!result.courses.length) {
    setMessage(result.calendarUnavailable ? '个人课表暂时不可用，未找到匹配课程。' : '未找到匹配的智云课程。')
   } else {
    setMessage(`找到 ${result.courses.length} 个教学班，请选择。`)
   }
  } catch (reason) {
   if (current !== request.current) return
   const text = errorMessage(reason)
   setMessage(loginHint(text))
   notify.error(reason, {title: '匹配智云课程失败'})
  } finally {
   setMatching(false)
  }
 }
 function clear() {
  request.current++
  onUpdate({
   zhiyunCourseId: undefined,
   zhiyunCourseLabel: undefined,
   subId: undefined,
   zhiyunReplayLabel: undefined,
   skipZhiyunMatch: undefined
  })
  setCourses([])
  setLessons(null)
  setMatching(false)
  setLoadingLessons(false)
  setMessage('')
 }
 const courseOptions = courses.map(course => ({value: course.id, label: courseLabel(course)}))
 const lessonOptions = (lessons || []).map(lesson => ({
  value: lesson.id,
  label: lesson.title || '未命名课次',
  description: [lesson.teacher, lesson.videoAvailable ? '可回放' : '暂无回放'].filter(Boolean).join(' · ')
 }))
 const courseText = entry.zhiyunCourseLabel || (entry.zhiyunCourseId ? `编号 ${entry.zhiyunCourseId}` : '未匹配')
 const replayText = entry.zhiyunReplayLabel || (entry.subId ? `编号 ${entry.subId}` : '未匹配')
 return (
  <div className="study-form-grid">
   <StudyField
    className="study-field--wide"
    label="智云课程与回放"
    hint="按课程名称自动匹配教学班和课堂回放；需要先在智云课堂登录。"
   >
    <div className="study-menu__actions">
     <Button
      variant="tertiary"
      size="sm"
      fullWidth
      isDisabled={busy || matching || loadingLessons || !searchTitle}
      onClick={() => void match()}
     >
      {matching ? '正在匹配…' : '自动匹配智云'}
     </Button>
     <Button
      variant="ghost"
      size="sm"
      fullWidth
      isDisabled={busy || matching || loadingLessons || (!entry.zhiyunCourseId && !entry.subId)}
      onClick={clear}
     >
      清除匹配
     </Button>
    </div>
    <small>课程：{courseText}</small>
    <small>回放：{replayText}</small>
    {loadingLessons ? <small>正在读取课堂回放…</small> : null}
    {message ? <small>{message}</small> : null}
   </StudyField>
   {courses.length > 1 ? (
    <StudyField className="study-field--wide" label="选择教学班">
     <SelectField
      label="选择教学班"
      searchable={courses.length > 8}
      isDisabled={matching || loadingLessons}
      value={entry.zhiyunCourseId || ''}
      options={courseOptions}
      onChange={next => {
       if (next) void chooseCourse(next)
      }}
     />
    </StudyField>
   ) : null}
   {lessons !== null ? (
    <StudyField className="study-field--wide" label="选择课堂回放">
     <SelectField
      label="选择课堂回放"
      searchable={lessons.length > 8}
      clearable
      value={entry.subId || ''}
      options={lessonOptions}
      onChange={next => {
       const lesson = lessons.find(item => item.id === next)
       onUpdate({
        subId: next || undefined,
        zhiyunReplayLabel: lesson ? lessonLabel(lesson) : undefined,
        skipZhiyunMatch: undefined
       })
      }}
     />
    </StudyField>
   ) : null}
  </div>
 )
}
