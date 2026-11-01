import {type ReactNode, useEffect, useRef, useState} from 'react'
import {Button, Checkbox, DateField, Disclosure, Input, SearchField, TextArea} from '@heroui/react'
import type {CalendarDateTime} from '@internationalized/date'
import {CalendarDays, Upload} from 'lucide-react'
import {ConfirmDialog} from '@renderer/ui/ConfirmDialog'
import {SelectField} from '@renderer/ui/SelectField'
import {Modal} from '@renderer/ui/UiParts'
import {WindowMenuButton} from '@renderer/ui/WindowPanel'
import {StudyField} from '@renderer/features/study/Field'
import {StudyNextStep} from '@renderer/features/study/StudyNextStep'
import {ScheduleImportStatus, type ScheduleImportState} from '@renderer/features/study/ScheduleImportStatus'
import {WeeklyFields, WeeklySchedule, periodName, weekdayName} from '@renderer/features/study/WeeklySchedule'
import {
 dateTimeInZone,
 formatDateTime,
 formatDay,
 formatTime,
 isoFromDateTime,
 validTimeZone
} from '@renderer/features/study/datetime'
import type {Course, Detail} from '@shared/types'
import type {StudyEntry} from '@shared/study'
import './schedule-workspace.css'

export type ScheduleIssue = {id: string; message: string; field?: string}

export type ScheduleProps = Readonly<{
 courses: Course[]
 entries: StudyEntry[] // NOSONAR: shared props type; this member is used by the sibling component.
 selected: string // NOSONAR: shared props type; this member is used by the sibling component.
 detail: Detail | null // NOSONAR: shared props type; this member is used by the sibling component.
 timezone: string
 busy: boolean
 importStatus: ScheduleImportState | null
 dirty: boolean // NOSONAR: shared props type; this member is used by the sibling component.
 issues: ScheduleIssue[] // NOSONAR: shared props type; this member is used by the sibling component.
 onSelect: (id: string) => void // NOSONAR: shared props type; this member is used by the sibling component.
 onAdd: (weekly?: boolean) => void // NOSONAR: shared props type; this member is used by the sibling component.
 onUpdate: (value: Partial<StudyEntry>) => void // NOSONAR: shared props type; this member is used by the sibling component.
 onDelete: () => void // NOSONAR: shared props type; this member is used by the sibling component.
 onClear: () => void
 onSave: () => void // NOSONAR: shared props type; this member is used by the sibling component.
 onCourseBook: () => void
 linkTarget: string
 onLinkShown: () => void
 onTimezone: (value: string) => void // NOSONAR: shared props type; this member is used by the sibling component.
 onParse: (text: string | null, timezone: string) => Promise<boolean> // NOSONAR: shared props type; this member is used by the sibling component.
}>

function courseName(courses: Course[], id?: string) {
 return courses.find(course => course.id === id)?.name || '未关联课程'
}

export function ScheduleMenu(props: ScheduleProps) {
 const {entries, selected, courses, timezone, issues, onSelect} = props
 const [query, setQuery] = useState('')
 if (!entries.length) return null
 const issueIds = new Set(issues.map(issue => issue.id))
 const keyword = query.trim().toLowerCase()
 const filtered = entries.filter(entry => {
  if (!keyword) return true
  return [entry.title, entry.chapter, entry.location, courseName(courses, entry.courseId)]
   .join(' ')
   .toLowerCase()
   .includes(keyword)
 })
 const sorted = [...filtered].sort((a, b) => {
  if (a.weekly && b.weekly) return (a.weekday ?? 8) - (b.weekday ?? 8) || (a.startPeriod ?? 31) - (b.startPeriod ?? 31)
  if (a.weekly) return -1
  if (b.weekly) return 1
  const left = a.endsAt ? Date.parse(a.endsAt) : Number.POSITIVE_INFINITY,
   right = b.endsAt ? Date.parse(b.endsAt) : Number.POSITIVE_INFINITY
  return left - right || a.title.localeCompare(b.title, 'zh-CN')
 })
 const groups: {label: string; rows: StudyEntry[]}[] = []
 for (const entry of sorted) {
  const label = entry.weekly ? weekdayName(entry.weekday) : formatDay(entry.endsAt, timezone)
  const group = groups.at(-1)
  if (group?.label === label) group.rows.push(entry)
  else groups.push({label, rows: [entry]})
 }
 return (
  <div className="study-menu">
   <SearchField aria-label="搜索课次" className="study-menu__search" fullWidth value={query} onChange={setQuery}>
    <SearchField.Group>
     <SearchField.SearchIcon />
     <SearchField.Input placeholder="搜索课次、章节或课程" />
     <SearchField.ClearButton />
    </SearchField.Group>
   </SearchField>
   <div className="study-entry-groups">
    {groups.map(group => (
     <section key={group.label} className="study-entry-group">
      <p className="study-entry-group__label">{group.label}</p>
      {group.rows.map(entry => (
       <WindowMenuButton
        key={entry.id}
        active={entry.id === selected}
        title={entry.title || '未命名课次'}
        note={
         <span className="study-entry-note">
          {entry.weekly ? (
           <>
            <span>{periodName(entry)}</span>
            <span>{entry.location || '教室待核对'}</span>
           </>
          ) : (
           <>
            <span>{courseName(courses, entry.courseId)}</span>
            <span>{entry.endsAt ? formatTime(entry.endsAt, timezone) : '时间待定'}</span>
           </>
          )}
          {entry.lectureId ? <span className="study-entry-linked">已关联</span> : null}
          {issueIds.has(entry.id) ? <span className="study-entry-warning">待完善</span> : null}
         </span>
        }
        onSelect={() => onSelect(entry.id)}
       >
        {entry.title || '未命名课次'}
       </WindowMenuButton>
      ))}
     </section>
    ))}
    {!filtered.length ? <p className="study-menu__hint">没有匹配的课次。</p> : null}
   </div>
  </div>
 )
}

export function ScheduleContent(props: ScheduleProps & {children: (toolsOpen: boolean) => ReactNode}) {
 const {entries, selected, busy, dirty, issues, importStatus, onSave, linkTarget, onLinkShown} = props
 const [toolsOpen, setToolsOpen] = useState(false)
 const editor = useRef<HTMLDivElement>(null)
 const tools = useRef<HTMLElement>(null)
 const entry = entries.find(item => item.id === selected)
 const empty = !entries.length
 const needsReview = dirty
  ? entries.filter(row => row.weekly && (!row.weekday || !row.startPeriod || !row.endPeriod || !row.location?.trim()))
  : []
 const reviewId = issues[0]?.id ?? needsReview[0]?.id
 const uploading = !!importStatus && importStatus.finishedAt === undefined
 const upload = () => void props.onParse(null, props.timezone)
 useEffect(() => {
  if (!entry || linkTarget !== entry.id) return
  if (!toolsOpen) {
   setToolsOpen(true)
   return
  }
  const target = entry.title.trim()
   ? tools.current?.querySelector<HTMLElement>('.study-match')
   : editor.current?.querySelector<HTMLInputElement>('[aria-label="课程名称"],[aria-label="课次名称"]')
  target?.focus({preventScroll: true})
  target?.scrollIntoView({block: 'center'})
  onLinkShown()
 }, [entry?.id, linkTarget, toolsOpen, onLinkShown])
 function openEntry(id: string) {
  props.onSelect(id)
  editor.current?.focus({preventScroll: true})
  editor.current?.scrollIntoView({block: 'start'})
 }
 function review() {
  if (!reviewId) return
  const target = entries.find(row => row.id === reviewId)
  if (!target) return
  const field =
   issues[0]?.field ??
   (!target.weekday ? '星期' : !target.startPeriod ? '开始节次' : !target.endPeriod ? '结束节次' : '教室')
  const advanced = field === '智云' || (field === '课程' && target.weekly)
  props.onSelect(reviewId)
  if (advanced) setToolsOpen(true)
  // Wait for the selected entry and any optional controls to mount before moving focus.
  requestAnimationFrame(() => {
   const region = advanced ? tools.current : editor.current
   const label = field === '智云' ? '智云课程搜索词' : field
   const fieldNode = region?.querySelector<HTMLElement>(`[aria-label="${CSS.escape(label)}"]`)
   const interactive = 'button:not(:disabled), input:not(:disabled), [tabindex="0"]'
   const control = fieldNode?.matches(interactive) ? fieldNode : fieldNode?.querySelector<HTMLElement>(interactive)
   const destination = control ?? region
   destination?.focus({preventScroll: true})
   destination?.scrollIntoView({block: 'center'})
  })
 }
 const uploadButton = !uploading && !importStatus?.error && (
  <Button
   className={empty && !dirty ? 'study-file-start__button' : undefined}
   variant={empty && !dirty ? 'primary' : 'secondary'}
   size={empty && !dirty ? 'md' : 'sm'}
   isDisabled={busy}
   onPress={upload}
  >
   <Upload size={16} aria-hidden="true" />
   {empty ? '上传课表图片' : '继续上传图片'}
  </Button>
 )
 const feedback = <ScheduleImportStatus status={importStatus} onRetry={upload} busy={busy} />
 const reviewRequired = issues.length > 0
 return (
  <>
   {uploading && <div className="study-file-start">{feedback}</div>}
   <div hidden={uploading}>
    {empty ? (
     <section className="study-file-start" aria-label="从图片导入课表">
      <div className="study-file-start__icon" aria-hidden="true">
       <CalendarDays size={26} />
      </div>
      <h3>{dirty ? '课表草稿已清空' : '从截图生成课表'}</h3>
      {!busy && !importStatus?.error && (
       <StudyNextStep>
        {dirty ? '点「保存空课表」，确认移除已保存的时段。' : '点「上传课表图片」，选一张 PNG 或 JPG 截图。'}
       </StudyNextStep>
      )}
      {dirty && (
       <Button
        className="study-file-start__button"
        variant={importStatus?.error ? 'secondary' : 'primary'}
        isDisabled={busy}
        onPress={onSave}
       >
        保存空课表
       </Button>
      )}
      {uploadButton}
      {!uploading && feedback}
     </section>
    ) : (
     <>
      <div className="study-savebar schedule-workspace__toolbar">
       <div>
        <strong>
         {dirty ? '有未保存的修改' : '课表已保存'} · {entries.length} 个时段
        </strong>
        {!busy && dirty && !importStatus?.error && (
         <StudyNextStep>
          {reviewRequired
           ? `点「查看待补项」，补全 ${issues.length} 处必填信息。`
           : needsReview.length
             ? `还有 ${needsReview.length} 个时段的星期、节次或教室待核对，可先点「保存课表」。`
             : '核对课程和时间后，点「保存课表」。'}
         </StudyNextStep>
        )}
       </div>
       <div className="schedule-workspace__actions">
        {uploadButton}
        {reviewId && (
         <Button
          variant={reviewRequired && !importStatus?.error ? 'primary' : 'tertiary'}
          size="sm"
          isDisabled={busy}
          onPress={review}
         >
          查看待补项
         </Button>
        )}
        <Button
         variant={dirty && !reviewRequired && !importStatus?.error ? 'primary' : 'secondary'}
         size="sm"
         isDisabled={busy || !dirty || reviewRequired}
         onPress={onSave}
        >
         保存课表
        </Button>
       </div>
      </div>
      {!uploading && feedback}
      <WeeklySchedule entries={entries} selected={selected} onSelect={openEntry} />
      {entry ? (
       <div ref={editor} tabIndex={-1} className="study-selected-entry" role="region" aria-label="当前课次">
        <ScheduleDetail
         key={entry.id}
         {...props}
         entry={entry}
         issues={issues.filter(issue => issue.id === entry.id)}
        />
       </div>
      ) : (
       <p className="study-menu__hint">选择一个课次即可编辑。</p>
      )}
     </>
    )}
    <section ref={tools} tabIndex={-1} className="schedule-workspace__tools">
     <Disclosure className="study-fold" isExpanded={toolsOpen} onExpandedChange={setToolsOpen}>
      <Disclosure.Heading>
       <Disclosure.Trigger className="study-fold__trigger">
        更多操作
        <Disclosure.Indicator />
       </Disclosure.Trigger>
      </Disclosure.Heading>
      <Disclosure.Content>
       <Disclosure.Body className="study-fold__body">
        <ScheduleActions
         {...props}
         entry={entry}
         onFinish={() => setToolsOpen(false)}
         onAdd={weekly => {
          props.onAdd(weekly)
          setToolsOpen(false)
          requestAnimationFrame(() => {
           editor.current?.scrollIntoView({block: 'start'})
           editor.current?.querySelector('input')?.focus({preventScroll: true})
          })
         }}
        />
        {entry && (
         <section
          id={'study-link-' + entry.id}
          className="schedule-workspace__materials"
          tabIndex={-1}
          aria-label="教材与本地课次"
         >
          <ScheduleMaterials {...props} entry={entry} />
         </section>
        )}
       </Disclosure.Body>
      </Disclosure.Content>
     </Disclosure>
     {props.children(toolsOpen)}
    </section>
   </div>
  </>
 )
}

function ScheduleActions(props: ScheduleProps & {entry?: StudyEntry; onFinish: () => void}) {
 const {entries, busy, courses, timezone, onAdd, onDelete, onTimezone, onParse, entry} = props
 const [confirmClear, setConfirmClear] = useState(false)
 const [confirmDelete, setConfirmDelete] = useState(false)
 const [confirmReplace, setConfirmReplace] = useState(false)
 const [importing, setImporting] = useState(false)
 const [text, setText] = useState('')
 async function recognizeText() {
  if (await onParse(text, timezone)) {
   setImporting(false)
   setText('')
   props.onFinish()
  }
 }
 return (
  <>
   <div className="schedule-workspace__actions">
    <Button variant="tertiary" size="sm" isDisabled={busy} onPress={() => setImporting(true)}>
     粘贴课表文字
    </Button>
    <Button variant="tertiary" size="sm" isDisabled={busy} onPress={() => onAdd(true)}>
     添加每周课程
    </Button>
    <Button variant="tertiary" size="sm" isDisabled={busy || !courses.length} onPress={() => onAdd(false)}>
     添加有具体日期的课次
    </Button>
    {entry && (
     <Button variant="ghost" size="sm" isDisabled={busy} onPress={() => setConfirmDelete(true)}>
      删除条目
     </Button>
    )}
    {!!entries.length && (
     <Button variant="danger-soft" size="sm" isDisabled={busy} onPress={() => setConfirmClear(true)}>
      清空课表
     </Button>
    )}
   </div>
   {!courses.length && <p className="study-menu__hint">添加有具体日期的课次前，请先在「课程对话」创建课程。</p>}
   {confirmClear && (
    <ConfirmDialog
     title="清空全部课表？"
     confirmLabel="确认清空"
     busy={busy}
     onCancel={() => setConfirmClear(false)}
     onConfirm={() => {
      setConfirmClear(false)
      props.onClear()
      props.onFinish()
     }}
    >
     将从草稿移除全部 {entries.length} 个课表时段及其关联。点击「保存空课表」才会更新已保存的课表。
    </ConfirmDialog>
   )}
   {confirmDelete ? (
    <ConfirmDialog
     title="删除课表条目？"
     busy={busy}
     onCancel={() => setConfirmDelete(false)}
     onConfirm={() => {
      setConfirmDelete(false)
      onDelete()
      props.onFinish()
     }}
    >
     只删除这条课表配置，点击「保存课表」后才会写入。不会删除关联的课次、聊天记录或已经写入教材的划线。
    </ConfirmDialog>
   ) : null}
   {confirmReplace ? (
    <ConfirmDialog
     title="替换当前课表草稿？"
     confirmLabel="识别并替换"
     onCancel={() => setConfirmReplace(false)}
     onConfirm={() => {
      setConfirmReplace(false)
      void recognizeText()
     }}
    >
     文字识别成功后会替换当前的 {entries.length} 条课表草稿。点击「保存课表」后才会写入。
    </ConfirmDialog>
   ) : null}
   {importing && !confirmReplace ? (
    <Modal title="粘贴课表文字" close={() => !busy && setImporting(false)}>
     <div className="study-import">
      <p className="study-menu__hint">粘贴包含日期、结束时间和章节的课表，并填写已有课程的名称。</p>
      <StudyField label="课表文本">
       <TextArea
        aria-label="课表文本"
        value={text}
        onChange={event => setText(event.target.value)}
        placeholder="例如：10 月 8 日 高等数学 第 3 讲 极限 20:00 结束"
       />
      </StudyField>
      <StudyField
       label="课表时区"
       hint="用于解释课表里的时间；保存时会写成带偏移的完整时间。"
       error={validTimeZone(timezone) ? undefined : '请输入有效的 IANA 时区，例如 Asia/Shanghai。'}
      >
       <Input aria-label="课表时区" value={timezone} onChange={event => onTimezone(event.target.value)} />
      </StudyField>
      <div className="study-import__footer">
       <Button variant="ghost" size="sm" isDisabled={busy} onClick={() => setImporting(false)}>
        取消
       </Button>
       <Button
        variant="primary"
        size="sm"
        isDisabled={busy || !text.trim() || !validTimeZone(timezone)}
        onClick={() => {
         if (entries.length) setConfirmReplace(true)
         else void recognizeText()
        }}
       >
        识别文字
       </Button>
      </div>
     </div>
    </Modal>
   ) : null}
  </>
 )
}

function CourseAndChapter({
 entry,
 courses,
 onUpdate
}: Pick<ScheduleProps, 'courses' | 'onUpdate'> & {entry: StudyEntry}) {
 return (
  <>
   <StudyField label={entry.weekly ? '本地课程（可选）' : '本地课程'}>
    <SelectField
     label="课程"
     searchable={courses.length > 8}
     value={entry.courseId || ''}
     clearable={entry.weekly}
     placeholder={entry.weekly ? '暂不关联' : '选择课程'}
     onChange={next => onUpdate({courseId: next, lectureId: undefined})}
     options={courses.map(course => ({value: course.id, label: course.name}))}
    />
   </StudyField>
   <StudyField label={entry.weekly ? '教材章节（可选）' : '教材章节'}>
    <Input
     aria-label="教材章节"
     value={entry.chapter}
     onChange={event => onUpdate({chapter: event.target.value})}
     placeholder="例如：第二章 极限与连续"
    />
   </StudyField>
  </>
 )
}

function ScheduleDetail({entry, courses, timezone, busy, onUpdate, issues}: ScheduleProps & {entry: StudyEntry}) {
 const [date, setDate] = useState<CalendarDateTime | null>(() => dateTimeInZone(entry.endsAt, timezone))
 useEffect(() => {
  setDate(dateTimeInZone(entry.endsAt, timezone))
 }, [entry.id, entry.endsAt, timezone])
 const setDateTime = (next: CalendarDateTime | null) => {
  setDate(next)
  if (next) onUpdate({endsAt: isoFromDateTime(next, timezone)})
  else onUpdate({endsAt: ''})
 }
 return (
  <div className="study-detail">
   <header className="study-detail__header">
    <div>
     <h3>核对当前课次</h3>
     <p>{entry.weekly ? `${weekdayName(entry.weekday)} · ${periodName(entry)}` : '有具体日期的课次'}</p>
    </div>
   </header>
   {issues.length > 0 && (
    <div className="study-issue-list">
     {issues.map(issue => (
      <small key={issue.message}>{issue.message}</small>
     ))}
    </div>
   )}
   <div className="study-form-section">
    <div className="study-form-grid">
     <StudyField label={entry.weekly ? '课程名称' : '课次名称'} className="study-field--wide">
      <Input
       aria-label={entry.weekly ? '课程名称' : '课次名称'}
       value={entry.title}
       onChange={event => onUpdate({title: event.target.value})}
       placeholder={entry.weekly ? '填写截图中的课程名称' : '例如：第 3 讲 极限'}
      />
     </StudyField>
     {entry.weekly ? (
      <WeeklyFields entry={entry} onUpdate={onUpdate} />
     ) : (
      <CourseAndChapter entry={entry} courses={courses} onUpdate={onUpdate} />
     )}
    </div>
   </div>
   {!entry.weekly && (
    <section className="study-form-section">
     <h4>时间</h4>
     <div className="study-form-grid">
      <StudyField
       label="课程结束时间"
       hint={`按 ${timezone || '课表时区'} 显示和保存`}
       error={validTimeZone(timezone) ? undefined : '课表时区无效，请在「粘贴课表文字」里修改。'}
      >
       <DateField
        aria-label="课程结束时间"
        granularity="minute"
        isDisabled={busy || !validTimeZone(timezone)}
        value={date}
        onChange={next => setDateTime((next as CalendarDateTime | null) || null)}
       >
        <DateField.Group>
         <DateField.Input>{segment => <DateField.Segment segment={segment} />}</DateField.Input>
        </DateField.Group>
       </DateField>
      </StudyField>
      <div className="study-field study-field--check">
       <Checkbox
        isSelected={!entry.endsAt}
        onChange={checked => {
         if (checked) {
          setDate(null)
          onUpdate({endsAt: ''})
         } else {
          const now = dateTimeInZone(new Date().toISOString(), timezone)
          setDateTime(now)
         }
        }}
       >
        <Checkbox.Content>
         <Checkbox.Control>
          <Checkbox.Indicator />
         </Checkbox.Control>
         时间待定
        </Checkbox.Content>
       </Checkbox>
       <small>
        {entry.endsAt && Number.isFinite(Date.parse(entry.endsAt))
         ? `将保存为 ${formatDateTime(entry.endsAt, timezone)}`
         : '补课或时间未定时勾选，不计算提醒时间。'}
       </small>
      </div>
     </div>
    </section>
   )}
  </div>
 )
}

function ScheduleMaterials({
 entry,
 courses,
 detail,
 busy,
 timezone,
 onUpdate,
 onCourseBook
}: ScheduleProps & {entry: StudyEntry}) {
 const lectures = entry.courseId === detail?.course.id ? detail?.lectures || [] : []
 return (
  <>
   <div className="study-course-book">
    <div>
     <strong>课程教材</strong>
     <p className="study-menu__hint">
      {entry.textbookId ? '本时段已选择教材，可预览或更换。' : '选择本地 PDF 即可阅读，有课堂资料时还可生成划线建议。'}
     </p>
    </div>
    <Button size="sm" variant="secondary" isDisabled={busy} onPress={onCourseBook}>
     {entry.textbookId ? '预览课程教材' : '选择课程教材'}
    </Button>
   </div>
   <h4 className="schedule-workspace__subheading">本地课次与教材章节</h4>
   <p className="study-menu__hint">
    准备生成教材划线时，先选本地课程，再关联已有课次。保存后会同步已关联课次的名称和聊天标题。
   </p>
   {!courses.length && (
    <p className="study-menu__hint">还没有本地课程。在「课程对话」新建课程并导入课堂资料后，返回这里选择课次。</p>
   )}
   {entry.courseId && detail?.course.id === entry.courseId && !lectures.length && (
    <p className="study-menu__hint">
     这门本地课程还没有课次。在「课程对话」选择「新建课次」并导入字幕或课件，再回来关联。
    </p>
   )}
   <div className="study-form-grid">
    {entry.weekly && <CourseAndChapter entry={entry} courses={courses} onUpdate={onUpdate} />}
    <StudyField label="关联已有课次" className="study-field--wide">
     <SelectField
      label="关联已有课次"
      searchable={lectures.length > 8}
      clearable
      isDisabled={!entry.courseId}
      placeholder="暂不关联"
      value={entry.lectureId || ''}
      onChange={next => onUpdate({lectureId: next || undefined})}
      options={lectures.map(lecture => ({
       value: lecture.id,
       label: lecture.title,
       description: lecture.created_at ? formatDateTime(lecture.created_at, timezone) : undefined
      }))}
     />
    </StudyField>
   </div>
  </>
 )
}
