import {Button, Disclosure, Input} from '@heroui/react'
import {useEffect, useState} from 'react'
import {periodName, weekdayName} from '@renderer/features/study/WeeklySchedule'
import {StudyField} from '@renderer/features/study/Field'
import {
 courseKey,
 courseLabel,
 lessonLabel,
 loginNeeded,
 matchesWeeklySlot,
 needsTermReview,
 useScheduleMatching
} from '@renderer/features/study/useScheduleMatching'
import {SelectField} from '@renderer/ui/SelectField'
import type {StudyEntry} from '@shared/study'
import type {Course, ZhiyunLesson} from '@shared/types'
import './schedule-matching.css'

export {useScheduleMatching}
type Matching = ReturnType<typeof useScheduleMatching>
type Props = Readonly<{
 matching: Matching
 entries: StudyEntry[]
 selected: string
 onSelect: (id: string) => void
 courses: Course[]
 busy: boolean
 toolsOpen: boolean
 openZhiyun: () => void
}>

const replayLabel = (lesson: ZhiyunLesson) =>
 [lessonLabel(lesson), lesson.videoAvailable ? '可回放' : '暂无回放'].join(' · ')
const slotLabel = (slot: StudyEntry) =>
 slot.weekly ? `${weekdayName(slot.weekday)} · ${periodName(slot)}` : slot.title
function groupStatus(m: Matching, group: Matching['groups'][number]) {
 const lookup = m.searches[group.key]
 if (lookup?.state === 'queued') return '等待查询'
 if (lookup?.state === 'loading') return '正在查询'
 if (lookup?.state === 'error') return loginNeeded(lookup.message) ? '需要验证登录' : '查询失败'
 const linked = group.slots.filter(row => row.zhiyunCourseId).length
 if (linked) return `已关联 ${linked}/${group.slots.length} 个时段`
 if (group.slots.every(row => m.skipped.has(row.id))) return '已跳过'
 if (lookup?.state === 'ready') return lookup.value.status === 'not-found' ? '未找到课程' : '待选择'
 return '尚未查询'
}

function needsChoice(m: Matching, key: string, slot: StudyEntry) {
 const search = m.searches[key]
 if (slot.zhiyunCourseId || slot.skipZhiyunMatch || search?.state !== 'ready') return false
 return (
  search.value.courses.length > 0 && (search.value.status === 'choose' || needsTermReview(slot, search.value.source))
 )
}

function rowErrors(m: Matching, group: Matching['groups'][number], row: StudyEntry) {
 const result = {authRequired: false, failed: false}
 if (row.skipZhiyunMatch) return result
 const lookups = [m.searches[group.key], ...(row.zhiyunCourseId ? [m.catalogs[row.zhiyunCourseId]] : [])]
 for (const lookup of lookups) {
  if (lookup.state !== 'error') continue
  if (loginNeeded(lookup.message)) result.authRequired = true
  else result.failed = true
 }
 return result
}

function scanMatchErrors(m: Matching, groups: Matching['groups']) {
 let authRequired = false
 let failed: StudyEntry | undefined
 for (const group of groups) {
  for (const row of group.slots) {
   const errors = rowErrors(m, group, row)
   authRequired = authRequired || errors.authRequired
   if (errors.failed && !failed) failed = row
  }
 }
 return {authRequired, failed}
}

export function ScheduleMatching(props: Props) {
 const {matching: m, entries, selected, busy, onSelect, toolsOpen} = props
 const [reviewSlot, setReviewSlot] = useState<string | null>(null)
 if (!entries.length) return null
 const group = m.groups.find(g => g.slots.some(row => row.id === selected))
 const slot = entries.find(row => row.id === selected)
 const waiting = m.groups.filter(g => m.searches[g.key]?.state === 'queued' || m.searches[g.key]?.state === 'loading')
 const resolved = m.groups.filter(g => m.searches[g.key]?.state === 'ready' || m.searches[g.key]?.state === 'error')
 const pending = m.groups.flatMap(g => g.slots.find(row => needsChoice(m, g.key, row)) ?? [])
 const {authRequired, failed} = scanMatchErrors(m, m.groups)
 const expanded = toolsOpen || reviewSlot === selected
 if (!expanded && !pending.length && !authRequired && !failed) return null
 let matchLabel = '匹配课表课程'
 if (waiting.length) matchLabel = '正在匹配…'
 else if (resolved.length) matchLabel = '重新匹配课表'
 function showSlot(id: string) {
  if (!toolsOpen) setReviewSlot(id)
  onSelect(id)
 }
 return (
  <section className="study-match" tabIndex={-1} aria-label="智云课程与回放">
   <div className="study-match__header">
    <strong>智云课程与回放</strong>
    {toolsOpen && (
     <Button
      size="sm"
      variant="tertiary"
      isDisabled={busy || !!waiting.length || !m.groups.length}
      onPress={() => void m.match(entries)}
     >
      {matchLabel}
     </Button>
    )}
    {!toolsOpen && expanded && (
     <Button size="sm" variant="ghost" onPress={() => setReviewSlot(null)}>
      收起
     </Button>
    )}
   </div>
   {authRequired && (
    <div className="study-match__controls">
     <output className="study-menu__hint">匹配教学班需要登录智云，课表可以先保存。</output>
     <Button size="sm" variant="secondary" onPress={props.openZhiyun}>
      登录智云课堂
     </Button>
     {!toolsOpen && (
      <Button size="sm" variant="ghost" isDisabled={busy || !!waiting.length} onPress={() => void m.match(entries)}>
       重新匹配
      </Button>
     )}
    </div>
   )}
   {pending.length > 0 && (
    <div className="study-match__controls">
     <span className="study-menu__hint">{pending.length} 门课需核对教师和学期。</span>
     <Button size="sm" variant="secondary" onPress={() => showSlot(pending[0].id)}>
      选择教学班
     </Button>
    </div>
   )}
   {failed && (
    <div className="study-match__controls">
     <output className="study-menu__hint">未能读取「{failed.title}」的智云信息。</output>
     <Button size="sm" variant="tertiary" onPress={() => failed && showSlot(failed.id)}>
      查看原因
     </Button>
    </div>
   )}
   {expanded && (
    <div className="study-match__details">
     {m.groups.length > 1 && (
      <StudyField label="查看各门课程的匹配状态">
       <SelectField
        label="查看各门课程的匹配状态"
        searchable={m.groups.length > 8}
        value={group?.key ?? ''}
        options={m.groups.map(g => ({value: g.key, label: g.title, description: groupStatus(m, g)}))}
        onChange={key => {
         const next = m.groups.find(g => g.key === key)
         if (next) showSlot((next.slots.find(row => needsChoice(m, key, row)) ?? next.slots[0]).id)
        }}
       />
      </StudyField>
     )}
     {slot && group ? (
      <SlotMatch key={slot.id} {...props} onSelect={showSlot} slot={slot} group={group} />
     ) : (
      <p className="study-menu__hint">填写课程名称后可以匹配智云。</p>
     )}
    </div>
   )}
  </section>
 )
}

type CourseGroup = Matching['groups'][number]
type CourseLookup = Matching['searches'][string]
type CourseResult = Extract<CourseLookup, {state: 'ready'}>['value']
type CatalogLookup = Matching['catalogs'][string]

function slotStatus(
 m: Matching,
 slot: StudyEntry,
 queryError: string,
 hasResult: boolean,
 courseId: string | undefined,
 searching: boolean,
 searchingCatalog: boolean
) {
 if (m.skipped.has(slot.id)) return '此时段已跳过匹配。'
 if (queryError) return queryError
 if (searchingCatalog) return '正在从全站查找其他教学班…'
 if (searching) return '正在查找智云课程，优先查询本月个人课表…'
 if (!hasResult && !courseId) return '尚未查询智云。'
 return ''
}

function replayStatus(
 replayError: string,
 catalog: CatalogLookup | undefined,
 lessons: ZhiyunLesson[],
 playable: ZhiyunLesson[]
) {
 if (replayError) return replayError
 if (catalog?.state === 'loading') return '正在查找这门课的课堂回放…'
 if (catalog?.state === 'ready') return `共 ${lessons.length} 个课次，${playable.length} 个可回放。`
 return '展开后会显示这个教学班的课堂回放。'
}

function useSlotSources(m: Matching, slot: StudyEntry, group: CourseGroup) {
 const search = m.searches[group.key]
 const result = search?.state === 'ready' ? search.value : undefined
 const courseId = slot.zhiyunCourseId
 const catalog = courseId ? m.catalogs[courseId] : undefined
 const lessons = catalog?.state === 'ready' ? catalog.value : []
 const playable = lessons.filter(lesson => lesson.videoAvailable)
 const aligned = new Set(playable.filter(lesson => matchesWeeklySlot(slot, lesson)).map(lesson => lesson.id))
 const currentReplay =
  result?.currentReplay?.courseId === courseId
   ? playable.find(lesson => lesson.id === result?.currentReplay?.subId)
   : undefined
 const candidates = result?.courses ?? []
 useEffect(() => {
  if (!courseId || slot.skipZhiyunMatch) return
  if (slot.zhiyunCourseLabel && !slot.zhiyunCourseLabel.includes(' · 教师待确认')) return
  if (!search) void m.search(group).catch(() => {})
  else if (
   result?.source === 'personal-calendar' &&
   !result.courses.some(course => course.id === courseId && course.teacher)
  )
   void m.search(group, group.title, true).catch(() => {})
 }, [group.key, courseId, slot.zhiyunCourseLabel, slot.skipZhiyunMatch, search])
 return {search, result, courseId, catalog, lessons, playable, aligned, currentReplay, candidates}
}

function courseChoices(
 candidates: CourseResult['courses'],
 courseId: string | undefined,
 slot: StudyEntry,
 group: CourseGroup,
 term: string | null,
 activeCourseId: string | undefined
) {
 const terms = [...new Set(candidates.map(course => course.term.trim()))]
 const filterTerm = term !== null && terms.includes(term) ? term : null
 const visible = candidates.filter(
  course => filterTerm === null || course.term.trim() === filterTerm || course.id === courseId
 )
 const courseOptions = visible.map(course => ({
  value: course.id,
  label: courseLabel(course, course.id === courseId ? slot.zhiyunCourseLabel : undefined)
 }))
 const openCourse = visible.find(course => course.id === activeCourseId && courseKey(course.title) === group.key)
 if (courseId && !courseOptions.some(item => item.value === courseId))
  courseOptions.unshift({value: courseId, label: slot.zhiyunCourseLabel || `${group.title} · 教师待确认`})
 return {terms, filterTerm, courseOptions, openCourse}
}

function replayChoices(lessons: ZhiyunLesson[], aligned: Set<string>, slot: StudyEntry) {
 const options = [...lessons]
  .sort((a, b) => Number(aligned.has(b.id)) - Number(aligned.has(a.id)))
  .map(lesson => ({
   value: lesson.id,
   label: replayLabel(lesson) + (aligned.has(lesson.id) ? ' · 与此时段相符' : '')
  }))
 if (slot.subId && !lessons.some(lesson => lesson.id === slot.subId))
  options.unshift({value: slot.subId, label: slot.zhiyunReplayLabel || '已关联回放 · 课次详情待确认'})
 return options
}

function SlotMatch({
 matching: m,
 busy,
 slot,
 group,
 courses,
 onSelect,
 toolsOpen,
 openZhiyun
}: Props & {slot: StudyEntry; group: CourseGroup}) {
 const [keyword, setKeyword] = useState('')
 const [term, setTerm] = useState<string | null>(null)
 const [pickedCourse, setPickedCourse] = useState('')
 const {result, courseId, catalog, lessons, playable, aligned, currentReplay, candidates, search} = useSlotSources(
  m,
  slot,
  group
 )
 const {terms, filterTerm, courseOptions, openCourse} = courseChoices(
  candidates,
  courseId,
  slot,
  group,
  term,
  result?.currentReplay?.courseId
 )
 const replayOptions = replayChoices(lessons, aligned, slot)
 const otherSlots = group.slots.filter(m.canFillCourse).length
 const queryError = search?.state === 'error' ? search.message : ''
 const replayError = catalog?.state === 'error' ? catalog.message : ''
 const localCourse = courses.find(course => course.id === slot.courseId)
 const searching = search?.state === 'queued' || search?.state === 'loading'
 const searchingCatalog = search?.state === 'loading' && search.catalogRequested === true
 function choose(id: string) {
  setPickedCourse(id)
  m.chooseCourse(
   slot,
   id,
   candidates.find(course => course.id === id)
  )
 }
 return (
  <div className="study-match__slot">
   <strong>
    {group.title} · {slotLabel(slot)}
   </strong>
   <SlotSwitch group={group} slot={slot} onSelect={onSelect} />
   <SlotStatus
    m={m}
    slot={slot}
    result={result}
    queryError={queryError}
    courseId={courseId}
    searching={searching}
    searchingCatalog={searchingCatalog}
   />
   <SlotAlerts
    m={m}
    busy={busy}
    group={group}
    courseId={courseId}
    queryError={queryError}
    replayError={replayError}
    toolsOpen={toolsOpen}
    openCourse={openCourse}
    choose={choose}
   />
   <SlotChoices
    busy={busy}
    slot={slot}
    group={group}
    courseId={courseId}
    terms={terms}
    filterTerm={filterTerm}
    setTerm={setTerm}
    courseOptions={courseOptions}
    choose={choose}
    pickedCourse={pickedCourse}
    otherSlots={otherSlots}
    m={m}
   />
   {courseId && toolsOpen && (
    <ReplaySection
     m={m}
     busy={busy}
     slot={slot}
     courseId={courseId}
     catalog={catalog}
     lessons={lessons}
     playable={playable}
     aligned={aligned}
     replayOptions={replayOptions}
     currentReplay={currentReplay}
     replayError={replayError}
    />
   )}
   {toolsOpen && (
    <SlotTools
     m={m}
     busy={busy}
     slot={slot}
     group={group}
     courseId={courseId}
     keyword={keyword}
     setKeyword={setKeyword}
     searching={searching}
     localCourse={localCourse}
     openZhiyun={openZhiyun}
    />
   )}
  </div>
 )
}

function SlotSwitch({
 group,
 slot,
 onSelect
}: Readonly<{group: CourseGroup; slot: StudyEntry; onSelect: (id: string) => void}>) {
 if (group.slots.length <= 1) return null
 return (
  <SelectField
   label="查看课表时段"
   value={slot.id}
   onChange={onSelect}
   options={group.slots.map(row => ({value: row.id, label: slotLabel(row)}))}
  />
 )
}

function SlotStatus({
 m,
 slot,
 result,
 queryError,
 courseId,
 searching,
 searchingCatalog
}: Readonly<{
 m: Matching
 slot: StudyEntry
 result: CourseResult | undefined
 queryError: string
 courseId: string | undefined
 searching: boolean
 searchingCatalog: boolean
}>) {
 const verifyTerm = needsTermReview(slot, result?.source)
 return (
  <output className="study-menu__hint">
   {slotStatus(m, slot, queryError, result !== undefined, courseId, searching, searchingCatalog)}
   {verifyTerm && '请核对教学班的学期。'}
   {result?.status === 'matched' &&
    !verifyTerm &&
    (result.matchedId === courseId ? '已匹配唯一同名课程。' : '找到唯一同名课程，可核对已有选择。')}
   {result?.status === 'not-found' &&
    (courseId ? '未找到同名候选，已有关联会保留。' : '没有找到对应课程，可换关键词或暂不关联。')}
   {result?.status === 'choose' && `有 ${result.courses.length} 个候选，待核对教师和学期。`}
   {result?.truncated && '候选较多，已展示部分结果。'}
   {result && (result.source === 'personal-calendar' ? ' 来源：本月个人课表。' : ' 来源：全站课程。')}
  </output>
 )
}

function SlotAlerts({
 m,
 busy,
 group,
 courseId,
 queryError,
 replayError,
 toolsOpen,
 openCourse,
 choose
}: Readonly<{
 m: Matching
 busy: boolean
 group: CourseGroup
 courseId: string | undefined
 queryError: string
 replayError: string
 toolsOpen: boolean
 openCourse: CourseResult['courses'][number] | undefined
 choose: (id: string) => void
}>) {
 return (
  <>
   {queryError && !loginNeeded(queryError) && (
    <Button size="sm" variant="secondary" isDisabled={busy} onPress={() => void m.search(group).catch(() => {})}>
     重试查找课程
    </Button>
   )}
   {courseId && replayError && !toolsOpen && !loginNeeded(replayError) && (
    <div className="study-match__controls">
     <output className="study-menu__hint">{replayError}</output>
     <Button
      size="sm"
      variant="secondary"
      isDisabled={busy}
      onPress={() => void m.catalog(courseId, true).catch(() => {})}
     >
      重试获取回放
     </Button>
    </div>
   )}
   {openCourse && openCourse.id !== courseId && (
    <Button size="sm" variant="secondary" isDisabled={busy} onPress={() => choose(openCourse.id)}>
     选用当前打开的智云课程 · {courseLabel(openCourse)}
    </Button>
   )}
  </>
 )
}

type CourseOption = {value: string; label: string}

function SlotChoices({
 m,
 busy,
 slot,
 group,
 courseId,
 terms,
 filterTerm,
 setTerm,
 courseOptions,
 choose,
 pickedCourse,
 otherSlots
}: Readonly<{
 m: Matching
 busy: boolean
 slot: StudyEntry
 group: CourseGroup
 courseId: string | undefined
 terms: string[]
 filterTerm: string | null
 setTerm: (value: string | null) => void
 courseOptions: CourseOption[]
 choose: (id: string) => void
 pickedCourse: string
 otherSlots: number
}>) {
 return (
  <>
   {terms.length > 1 && (
    <StudyField label="按学期筛选">
     <SelectField
      label="按学期筛选"
      value={filterTerm === null ? 'all' : 'term:' + filterTerm}
      isDisabled={busy}
      options={[
       {value: 'all', label: '全部学期'},
       ...terms.map(value => ({value: 'term:' + value, label: value || '未标注学期'}))
      ]}
      onChange={value => setTerm(value === 'all' ? null : value.slice(5))}
     />
    </StudyField>
   )}
   {courseOptions.length > 0 && (
    <StudyField label="智云课程">
     <SelectField
      label="智云课程"
      placeholder="请选择课程"
      searchable={courseOptions.length > 8}
      isDisabled={busy}
      value={courseId || ''}
      options={courseOptions}
      onChange={choose}
     />
    </StudyField>
   )}
   {courseId && pickedCourse === courseId && otherSlots > 0 && (
    <Button size="sm" variant="secondary" isDisabled={busy} onPress={() => m.fillCourse(group, slot)}>
     将此课程用于其他未关联时段（{otherSlots}）
    </Button>
   )}
  </>
 )
}

function ReplaySection({
 m,
 busy,
 slot,
 courseId,
 catalog,
 lessons,
 playable,
 aligned,
 replayOptions,
 currentReplay,
 replayError
}: Readonly<{
 m: Matching
 busy: boolean
 slot: StudyEntry
 courseId: string
 catalog: CatalogLookup | undefined
 lessons: ZhiyunLesson[]
 playable: ZhiyunLesson[]
 aligned: Set<string>
 replayOptions: CourseOption[]
 currentReplay: ZhiyunLesson | undefined
 replayError: string
}>) {
 return (
  <Disclosure
   onExpandedChange={open => {
    if (open && !catalog) void m.catalog(courseId).catch(() => {})
   }}
  >
   <Disclosure.Heading>
    <Disclosure.Trigger className="study-match__trigger">
     选择具体回放 · {slot.subId ? '已关联回放' : '只关联课程'} <Disclosure.Indicator />
    </Disclosure.Trigger>
   </Disclosure.Heading>
   <Disclosure.Content>
    <Disclosure.Body className="study-match__details">
     <output className="study-menu__hint">
      {replayStatus(replayError, catalog, lessons, playable)}
      {slot.weekly && !slot.subId && '这是每周重复的时段，可选定具体回放，也可只关联课程。'}
      {aligned.size > 0 && `有 ${aligned.size} 个可播放回放的星期和节次与此时段相符，已优先列出。`}
     </output>
     {replayOptions.length > 0 && (
      <StudyField label="此时段的具体回放">
       <SelectField
        label="此时段的具体回放"
        placeholder="只关联课程"
        clearable
        searchable={replayOptions.length > 8}
        isDisabled={busy}
        value={slot.subId || ''}
        options={replayOptions}
        disabledKeys={lessons.filter(item => !item.videoAvailable && item.id !== slot.subId).map(item => item.id)}
        onChange={id => m.chooseReplay(slot, id)}
       />
      </StudyField>
     )}
     {currentReplay && currentReplay.id !== slot.subId && (
      <Button size="sm" variant="secondary" isDisabled={busy} onPress={() => m.chooseReplay(slot, currentReplay.id)}>
       使用当前打开的回放 · {currentReplay.title}
      </Button>
     )}
     {(catalog?.state === 'ready' || replayError) && (
      <Button
       size="sm"
       variant="tertiary"
       isDisabled={busy || catalog?.state === 'loading'}
       onPress={() => void m.catalog(courseId, true).catch(() => {})}
      >
       {replayError ? '重试获取回放' : '刷新回放列表'}
      </Button>
     )}
    </Disclosure.Body>
   </Disclosure.Content>
  </Disclosure>
 )
}

function SlotTools({
 m,
 busy,
 slot,
 group,
 courseId,
 keyword,
 setKeyword,
 searching,
 localCourse,
 openZhiyun
}: Readonly<{
 m: Matching
 busy: boolean
 slot: StudyEntry
 group: CourseGroup
 courseId: string | undefined
 keyword: string
 setKeyword: (value: string) => void
 searching: boolean
 localCourse: Course | undefined
 openZhiyun: () => void
}>) {
 return (
  <div className="study-match__details">
   <p className="study-menu__hint">
    {localCourse ? `本地课程 · ${localCourse.name}。` : ''}登录智云后可匹配教学班，保存课表时写入关联。
   </p>
   <div className="study-match__controls">
    <Input
     aria-label="智云课程搜索词"
     value={keyword}
     onChange={e => setKeyword(e.target.value)}
     placeholder={group.title}
     maxLength={100}
    />
    <Button
     size="sm"
     variant="tertiary"
     isDisabled={busy || searching}
     onPress={() => void m.search(group, keyword.trim() || group.title, true).catch(() => {})}
    >
     搜索全站其他教学班
    </Button>
   </div>
   <div className="study-match__controls">
    {m.skipped.has(slot.id) ? (
     <Button size="sm" variant="tertiary" isDisabled={busy} onPress={() => m.resume(slot)}>
      恢复匹配
     </Button>
    ) : (
     <Button size="sm" variant="tertiary" isDisabled={busy} onPress={() => m.chooseCourse(slot, '')}>
      {courseId || slot.subId ? '解除此时段的智云关联' : '跳过此时段'}
     </Button>
    )}
    <Button size="sm" variant="tertiary" isDisabled={busy} onPress={openZhiyun}>
     打开智云课堂
    </Button>
   </div>
  </div>
 )
}
