import {useEffect, useRef, useState, type Dispatch, type SetStateAction} from 'react'
import {errorMessage} from '@renderer/ui/ToastHost'
import {dateTimeInZone} from '@renderer/features/study/datetime'
import type {StudyEntry} from '@shared/study'
import type {Course, ZhiyunCourse, ZhiyunCourseMatch, ZhiyunLesson} from '@shared/types'

type Lookup<T> =
 | {state: 'queued' | 'loading'; catalogRequested?: boolean}
 | {state: 'ready'; value: T}
 | {state: 'error'; message: string}
type Lookups<T> = Record<string, Lookup<T>>
type CourseGroup = {title: string; key: string; slots: StudyEntry[]}
export const courseKey = (text: string) => text.normalize('NFKC').replace(/\s+/gu, '').toLowerCase()
export const courseLabel = (course: ZhiyunCourse, saved?: string) =>
 !course.teacher && saved
  ? saved
  : [course.title, course.teacher || '教师待确认', course.term].filter(Boolean).join(' · ')
export const lessonLabel = (lesson: ZhiyunLesson) => [lesson.title, lesson.teacher].filter(Boolean).join(' · ')
function refreshLabels(
 rows: StudyEntry[],
 field: 'zhiyunCourseLabel' | 'zhiyunReplayLabel',
 labelFor: (row: StudyEntry) => string | undefined
) {
 let changed = false
 const next = rows.map(row => {
  const label = labelFor(row)
  if (!label || label === row[field]) return row
  changed = true
  return {...row, [field]: label}
 })
 return changed ? next : rows
}
function replayLabelFor(replay: ZhiyunLesson | undefined, row: StudyEntry, id: string) {
 if (replay) return lessonLabel(replay)
 if (row.subId === id) return row.zhiyunReplayLabel
 return undefined
}
const searchTitle = (slot: StudyEntry, courses: Course[]) =>
 (slot.weekly ? slot.title : courses.find(course => course.id === slot.courseId)?.name || slot.title).trim()
export const loginNeeded = (message: string) => /请先.*登录|返回了登录页|登录已过期|登录状态已失效/.test(message)

function groupCourses(entries: StudyEntry[], courses: Course[]) {
 const groups = new Map<string, CourseGroup>()
 for (const slot of entries) {
  const title = searchTitle(slot, courses),
   key = courseKey(title)
  if (!key) continue
  const group = groups.get(key)
  if (group) group.slots.push(slot)
  else groups.set(key, {title, key, slots: [slot]})
 }
 return [...groups.values()]
}

function campusDate(slot: StudyEntry) {
 if (!slot.weekly && slot.endsAt) return dateTimeInZone(slot.endsAt, 'Asia/Shanghai')?.toString().slice(0, 10)
}

export function needsTermReview(slot: StudyEntry, source?: ZhiyunCourseMatch['source']) {
 if (source !== 'personal-calendar' || slot.weekly || !slot.endsAt) return false
 const date = campusDate(slot)
 const month = dateTimeInZone(new Date().toISOString(), 'Asia/Shanghai')?.toString().slice(0, 7)
 return !date || !month || !date.startsWith(month)
}

function replayDate(title: string) {
 const matches = [...title.matchAll(/(?<!\d)(\d{4})[-/.年](\d{1,2})[-/.月](\d{1,2})(?!\d)/g)]
 if (matches.length !== 1) return
 const [, year, month, day] = matches[0]
 const value = [year, month.padStart(2, '0'), day.padStart(2, '0')].join('-')
 const date = new Date(Date.UTC(Number(year), Number(month) - 1, Number(day)))
 return date.toISOString().slice(0, 10) === value ? date : undefined
}

export function matchesWeeklySlot(slot: StudyEntry, lesson: ZhiyunLesson) {
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

function datedReplay(date: string, lessons: ZhiyunLesson[]) {
 const sameDay = lessons.filter(lesson => replayDate(lesson.title)?.toISOString().slice(0, 10) === date)
 if (sameDay.length === 1 && sameDay[0].videoAvailable) return sameDay[0]
}

export function useScheduleMatching(
 entries: StudyEntry[],
 setEntries: Dispatch<SetStateAction<StudyEntry[]>>,
 courses: Course[]
) {
 const [searches, setSearches] = useState<Lookups<ZhiyunCourseMatch>>({})
 const [catalogs, setCatalogs] = useState<Lookups<ZhiyunLesson[]>>({})
 const skipped = new Set(entries.filter(row => row.skipZhiyunMatch).map(row => row.id))
 const manual = useRef(new Set<string>()),
  epoch = useRef(0)
 const searchesInFlight = useRef(new Map<string, Promise<ZhiyunCourseMatch>>())
 const latestByGroup = useRef(new Map<string, Promise<ZhiyunCourseMatch>>())
 const catalogCache = useRef(new Map<string, Promise<ZhiyunLesson[]>>())
 const groups = groupCourses(entries, courses)
 useEffect(
  () => () => {
   epoch.current++
  },
  []
 )

 function stop() {
  epoch.current++
  searchesInFlight.current.clear()
  latestByGroup.current.clear()
  catalogCache.current.clear()
  setSearches(state =>
   Object.fromEntries(Object.entries(state).filter(([, item]) => item.state === 'ready' || item.state === 'error'))
  )
  setCatalogs(state =>
   Object.fromEntries(Object.entries(state).filter(([, item]) => item.state === 'ready' || item.state === 'error'))
  )
 }
 function reset() {
  stop()
  manual.current.clear()
  setSearches({})
  setCatalogs({})
 }
 function catalog(id: string, refresh = false) {
  const current = epoch.current
  const cached = catalogCache.current.get(id)
  if (cached && !refresh) return cached
  setCatalogs(state => ({...state, [id]: {state: 'loading'}}))
  const request = window.assistant.invoke<ZhiyunLesson[]>('zhiyun:lessons', {courseId: id})
  catalogCache.current.set(id, request)
  void request.then(
   lessons => {
    if (epoch.current !== current || catalogCache.current.get(id) !== request) return
    setCatalogs(state => ({...state, [id]: {state: 'ready', value: lessons}}))
    const labels = new Map(lessons.map(lesson => [lesson.id, lessonLabel(lesson)]))
    setEntries(rows =>
     refreshLabels(rows, 'zhiyunReplayLabel', row =>
      row.zhiyunCourseId === id && row.subId ? labels.get(row.subId) : undefined
     )
    )
   },
   error => {
    if (epoch.current !== current || catalogCache.current.get(id) !== request) return
    catalogCache.current.delete(id)
    setCatalogs(state => ({...state, [id]: {state: 'error', message: errorMessage(error)}}))
   }
  )
  return request
 }
 async function search(group: CourseGroup, keyword = group.title, catalogOnly = false) {
  const current = epoch.current
  setSearches(state => ({...state, [group.key]: {state: 'loading', catalogRequested: catalogOnly}}))
  const requestKey = (catalogOnly ? 'catalog:' : 'personal:') + courseKey(keyword)
  const request =
   searchesInFlight.current.get(requestKey) ??
   window.assistant.invoke<ZhiyunCourseMatch>('zhiyun:match-course', {title: keyword, catalogOnly})
  searchesInFlight.current.set(requestKey, request)
  latestByGroup.current.set(group.key, request)
  try {
   const result = await request
   if (current !== epoch.current || latestByGroup.current.get(group.key) !== request) return
   setSearches(state => ({...state, [group.key]: {state: 'ready', value: result}}))
   // Old timetables contain IDs only. Keep the selected teaching section even when its title is shared.
   const candidates = new Map(result.courses.map(course => [course.id, course]))
   setEntries(rows =>
    refreshLabels(rows, 'zhiyunCourseLabel', row => {
     const course = row.zhiyunCourseId ? candidates.get(row.zhiyunCourseId) : undefined
     return course && courseLabel(course, row.zhiyunCourseLabel)
    })
   )
   return result
  } catch (error) {
   if (current === epoch.current && latestByGroup.current.get(group.key) === request)
    setSearches(state => ({...state, [group.key]: {state: 'error', message: errorMessage(error)}}))
   throw error
  } finally {
   if (searchesInFlight.current.get(requestKey) === request) searchesInFlight.current.delete(requestKey)
  }
 }

 function autoLinkable(
  row: StudyEntry,
  ids: Set<string>,
  manual: Set<string>,
  source: ZhiyunCourseMatch['source'],
  groupKey: string,
  courses: Course[]
 ) {
  if (!ids.has(row.id) || manual.has(row.id) || row.skipZhiyunMatch || row.zhiyunCourseId || row.subId) return false
  return !needsTermReview(row, source) && courseKey(searchTitle(row, courses)) === groupKey
 }

 function withDatedReplays(
  state: StudyEntry[],
  id: string,
  ids: Set<string>,
  lessons: ZhiyunLesson[],
  request: Promise<ZhiyunLesson[]>,
  manual: Set<string>
 ) {
  if (catalogCache.current.get(id) !== request) return state
  // Only suggest a dated replay when the catalog and the timetable each have a single lesson that day.
  const dates = new Map<string, number>()
  for (const row of state) {
   const date = row.zhiyunCourseId === id ? campusDate(row) : undefined
   if (date) dates.set(date, (dates.get(date) || 0) + 1)
  }
  return state.map(row => {
   if (!ids.has(row.id) || row.zhiyunCourseId !== id || row.subId || row.skipZhiyunMatch || manual.has(row.id))
    return row
   const date = campusDate(row)
   const replay = date && dates.get(date) === 1 ? datedReplay(date, lessons) : undefined
   return replay ? {...row, subId: replay.id, zhiyunReplayLabel: lessonLabel(replay)} : row
  })
 }

 async function match(rows: StudyEntry[], includeSkipped = false) {
  const current = epoch.current
  const queue = groupCourses(includeSkipped ? rows : rows.filter(row => !row.skipZhiyunMatch), courses)
  setSearches(state => ({...state, ...Object.fromEntries(queue.map(group => [group.key, {state: 'queued' as const}]))}))
  let loginError = ''
  async function processGroup(group: CourseGroup, requestEpoch: number) {
   const result = await search(group)
   if (requestEpoch !== epoch.current) return
   if (!result) return
   const matched =
    result.status === 'matched' ? result.courses.find(course => course.id === result.matchedId) : undefined
   const ids = new Set(group.slots.map(row => row.id))
   if (matched)
    setEntries(state =>
     state.map(row =>
      requestEpoch === epoch.current && autoLinkable(row, ids, manual.current, result.source, group.key, courses)
       ? {...row, zhiyunCourseId: matched.id, zhiyunCourseLabel: courseLabel(matched)}
       : row
     )
    )
   const courseIds = new Set(
    group.slots
     .map(row => row.zhiyunCourseId || (needsTermReview(row, result.source) ? undefined : matched?.id))
     .filter((id): id is string => !!id)
   )
   for (const id of courseIds) {
    const request = catalog(id)
    const lessons = await request
    if (requestEpoch !== epoch.current) return
    setEntries(state => withDatedReplays(state, id, ids, lessons, request, manual.current))
   }
  }
  const worker = async () => {
   while (queue.length && current === epoch.current) {
    const group = queue.shift()!
    if (loginError) {
     setSearches(state => ({...state, [group.key]: {state: 'error', message: loginError}}))
     continue
    }
    try {
     await processGroup(group, current)
    } catch (error) {
     const message = errorMessage(error)
     if (loginNeeded(message)) loginError = message
    }
   }
  }
  await Promise.all([worker(), worker()])
 }

 function chooseCourse(slot: StudyEntry, id: string, course?: ZhiyunCourse) {
  manual.current.add(slot.id)
  setEntries(state =>
   state.map(row => {
    if (row.id !== slot.id) return row
    const sameCourse = row.zhiyunCourseId === id
    const savedLabel = sameCourse ? row.zhiyunCourseLabel : undefined
    return {
     ...row,
     skipZhiyunMatch: id ? undefined : true,
     zhiyunCourseId: id || undefined,
     zhiyunCourseLabel: course?.id === id ? courseLabel(course, savedLabel) : savedLabel,
     subId: sameCourse ? row.subId : undefined,
     zhiyunReplayLabel: sameCourse ? row.zhiyunReplayLabel : undefined
    }
   })
  )
  if (id) void catalog(id).catch(() => {})
 }
 function chooseReplay(slot: StudyEntry, id: string) {
  manual.current.add(slot.id)
  const lookup = slot.zhiyunCourseId ? catalogs[slot.zhiyunCourseId] : undefined
  const replay = lookup?.state === 'ready' ? lookup.value.find(lesson => lesson.id === id) : undefined
  setEntries(state =>
   state.map(row =>
    row.id === slot.id && row.zhiyunCourseId === slot.zhiyunCourseId
     ? {
        ...row,
        subId: id || undefined,
        zhiyunReplayLabel: replayLabelFor(replay, row, id)
       }
     : row
   )
  )
 }
 function canFillCourse(row: StudyEntry) {
  return !row.zhiyunCourseId && !row.subId && !row.skipZhiyunMatch && !manual.current.has(row.id)
 }
 function fillCourse(group: CourseGroup, source: StudyEntry) {
  const courseId = source.zhiyunCourseId
  if (!courseId) return
  const targets = new Set(group.slots.filter(canFillCourse).map(row => row.id))
  for (const id of targets) manual.current.add(id)
  setEntries(state => {
   if (!state.some(row => row.id === source.id && row.zhiyunCourseId === courseId)) return state
   return state.map(row => {
    if (
     !targets.has(row.id) ||
     row.zhiyunCourseId ||
     row.subId ||
     row.skipZhiyunMatch ||
     courseKey(searchTitle(row, courses)) !== group.key
    )
     return row
    return {...row, zhiyunCourseId: courseId, zhiyunCourseLabel: source.zhiyunCourseLabel}
   })
  })
 }
 function resume(slot: StudyEntry) {
  manual.current.delete(slot.id)
  setEntries(state => state.map(row => (row.id === slot.id ? {...row, skipZhiyunMatch: undefined} : row)))
  void match([slot], true)
 }
 return {
  groups,
  searches,
  catalogs,
  skipped,
  catalog,
  search,
  match,
  stop,
  reset,
  chooseCourse,
  chooseReplay,
  resume,
  canFillCourse,
  fillCourse
 }
}
