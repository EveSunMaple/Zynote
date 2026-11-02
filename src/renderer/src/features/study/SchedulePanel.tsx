import {useEffect, useState} from 'react'
import {Button, Checkbox, DateField, Disclosure, Input, ScrollShadow, SearchField, TextArea} from '@heroui/react'
import type {CalendarDateTime} from '@internationalized/date'
import {ConfirmDialog} from '@renderer/ui/ConfirmDialog'
import {SelectField} from '@renderer/ui/SelectField'
import {Modal} from '@renderer/ui/UiParts'
import {WindowMenuButton} from '@renderer/ui/WindowPanel'
import {StudyField} from '@renderer/features/study/Field'
import {ZhiyunSync} from '@renderer/features/study/ZhiyunSync'
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

export type ScheduleIssue = {id: string; message: string}

export type ScheduleProps = Readonly<{
 courses: Course[]
 entries: StudyEntry[] // NOSONAR: shared props type; this member is used by the sibling component.
 selected: string // NOSONAR: shared props type; this member is used by the sibling component.
 detail: Detail | null // NOSONAR: shared props type; this member is used by the sibling component.
 timezone: string
 busy: boolean
 dirty: boolean // NOSONAR: shared props type; this member is used by the sibling component.
 issues: ScheduleIssue[] // NOSONAR: shared props type; this member is used by the sibling component.
 onSelect: (id: string) => void // NOSONAR: shared props type; this member is used by the sibling component.
 onAdd: () => void // NOSONAR: shared props type; this member is used by the sibling component.
 onUpdate: (value: Partial<StudyEntry>) => void // NOSONAR: shared props type; this member is used by the sibling component.
 onDelete: () => void // NOSONAR: shared props type; this member is used by the sibling component.
 onDeleteMany: (ids: string[]) => void // NOSONAR: shared props type; this member is used by the sibling component.
 onSave: () => void // NOSONAR: shared props type; this member is used by the sibling component.
 onTimezone: (value: string) => void // NOSONAR: shared props type; this member is used by the sibling component.
 onParse: (text: string, timezone: string) => Promise<boolean> // NOSONAR: shared props type; this member is used by the sibling component.
 onParseImage: () => Promise<boolean> // NOSONAR: shared props type; this member is used by the sibling component.
}>

const weekdays = ['周一', '周二', '周三', '周四', '周五', '周六', '周日']
const dayOptions = weekdays.map((label, index) => ({value: String(index + 1), label}))
const periodOptions = Array.from({length: 30}, (_, index) => ({value: String(index + 1), label: `第 ${index + 1} 节`}))

function courseName(courses: Course[], id: string) {
 return courses.find(course => course.id === id)?.name || '未知课程'
}

function weekdayName(day?: number) {
 return weekdays[(day || 0) - 1] || '星期待核对'
}

function periodName(entry: StudyEntry) {
 const {startPeriod, endPeriod} = entry
 if (!startPeriod && !endPeriod) return '节次待核对'
 if (startPeriod === endPeriod) return `第 ${startPeriod} 节`
 return `第 ${startPeriod ?? '?'}–${endPeriod ?? '?'} 节`
}

function WeeklyFields({
 entry,
 onUpdate
}: Readonly<{entry: StudyEntry; onUpdate: (value: Partial<StudyEntry>) => void}>) {
 return (
  <>
   <StudyField label="星期">
    <SelectField
     label="星期"
     placeholder="待核对"
     clearable
     options={dayOptions}
     value={String(entry.weekday ?? '')}
     onChange={value => onUpdate({weekday: Number(value) || undefined})}
    />
   </StudyField>
   <StudyField label="教室">
    <Input
     aria-label="教室"
     placeholder="例如：东1-201"
     maxLength={200}
     value={entry.location ?? ''}
     onChange={event => onUpdate({location: event.target.value})}
    />
   </StudyField>
   <StudyField label="开始节次">
    <SelectField
     label="开始节次"
     placeholder="待核对"
     clearable
     options={periodOptions}
     value={String(entry.startPeriod ?? '')}
     onChange={value => onUpdate({startPeriod: Number(value) || undefined})}
    />
   </StudyField>
   <StudyField label="结束节次">
    <SelectField
     label="结束节次"
     placeholder="待核对"
     clearable
     options={periodOptions}
     value={String(entry.endPeriod ?? '')}
     onChange={value => onUpdate({endPeriod: Number(value) || undefined})}
    />
   </StudyField>
  </>
 )
}

export function ScheduleMenu(props: ScheduleProps) {
 const {
  entries,
  selected,
  courses,
  timezone,
  busy,
  dirty,
  issues,
  onSelect,
  onAdd,
  onDeleteMany,
  onTimezone,
  onParse,
  onParseImage
 } = props
 const issueIds = new Set(issues.map(issue => issue.id))
 const [query, setQuery] = useState(''),
  [importing, setImporting] = useState(false),
  [confirmReplace, setConfirmReplace] = useState(false),
  [text, setText] = useState(''),
  [managing, setManaging] = useState(false),
  [batch, setBatch] = useState<Set<string>>(new Set()),
  [confirmBatch, setConfirmBatch] = useState(false)
 useEffect(() => {
  setBatch(current => new Set([...current].filter(id => entries.some(entry => entry.id === id))))
 }, [entries])
 const toggleBatch = (id: string, checked: boolean) => {
  setBatch(current => {
   const next = new Set(current)
   if (checked) next.add(id)
   else next.delete(id)
   return next
  })
 }
 const allBatchSelected = entries.length > 0 && entries.every(entry => batch.has(entry.id))
 const keyword = query.trim().toLowerCase()
 const filtered = entries.filter(entry => {
  if (!keyword) return true
  return [entry.title, entry.chapter, courseName(courses, entry.courseId)].join(' ').toLowerCase().includes(keyword)
 })
 const weekly = filtered
  .filter(entry => entry.weekly)
  .sort(
   (a, b) =>
    (a.weekday ?? 99) - (b.weekday ?? 99) ||
    (a.startPeriod ?? 99) - (b.startPeriod ?? 99) ||
    a.title.localeCompare(b.title, 'zh-CN')
  )
 const dated = filtered
  .filter(entry => !entry.weekly)
  .sort((a, b) => {
   const left = a.endsAt ? Date.parse(a.endsAt) : Number.POSITIVE_INFINITY,
    right = b.endsAt ? Date.parse(b.endsAt) : Number.POSITIVE_INFINITY
   return left - right || a.title.localeCompare(b.title, 'zh-CN')
  })
 const groups: {label: string; rows: StudyEntry[]}[] = weekly.length ? [{label: '每周课程', rows: weekly}] : []
 for (const entry of dated) {
  const label = formatDay(entry.endsAt, timezone)
  const group = groups.at(-1)
  if (group?.label === label) group.rows.push(entry)
  else groups.push({label, rows: [entry]})
 }
 const openImport = () => {
  if (dirty) setConfirmReplace(true)
  else setImporting(true)
 }
 return (
  <div className="study-menu study-menu--fixed">
   <div className="study-menu__fixed">
    <SearchField aria-label="搜索课次" className="study-menu__search" fullWidth value={query} onChange={setQuery}>
     <SearchField.Group>
      <SearchField.SearchIcon />
      <SearchField.Input placeholder="搜索课次、章节或课程" />
      <SearchField.ClearButton />
     </SearchField.Group>
    </SearchField>
    <Button variant="primary" size="sm" fullWidth isDisabled={busy} onClick={() => void onParseImage()}>
     上传课表图片
    </Button>
    <div className="study-menu__actions">
     <Button variant="tertiary" size="sm" fullWidth isDisabled={busy} onClick={openImport}>
      识别文字课表
     </Button>
     <Button variant="tertiary" size="sm" fullWidth isDisabled={busy || !courses.length} onClick={onAdd}>
      手动添加
     </Button>
    </div>
    <div className="study-menu__manage">
     <Button
      variant="tertiary"
      size="sm"
      fullWidth
      isDisabled={busy || !entries.length}
      onClick={() => {
       setManaging(value => !value)
       setBatch(new Set())
      }}
     >
      {managing ? '完成' : '批量修改课表'}
     </Button>
     {managing ? (
      <div className="study-menu__actions">
       <Button
        variant="ghost"
        size="sm"
        isDisabled={busy}
        onClick={() => setBatch(allBatchSelected ? new Set() : new Set(entries.map(entry => entry.id)))}
       >
        {allBatchSelected ? '取消全选' : '全选'}
       </Button>
       <Button variant="danger-soft" size="sm" isDisabled={busy || !batch.size} onClick={() => setConfirmBatch(true)}>
        删除所选（{batch.size}）
       </Button>
      </div>
     ) : null}
    </div>
   </div>
   <ScrollShadow className="study-menu__scroll" orientation="vertical">
    {entries.length ? (
     <div className="study-entry-groups">
      {groups.map(group => (
       <section key={group.label} className="study-entry-group">
        <p className="study-entry-group__label">{group.label}</p>
        {group.rows.map(entry => (
         <div className="study-entry-row" key={entry.id}>
          {managing ? (
           <Checkbox
            className="study-entry-check"
            aria-label={`选择课表条目 ${entry.title || '未命名课次'}`}
            isSelected={batch.has(entry.id)}
            onChange={checked => toggleBatch(entry.id, checked)}
           >
            <Checkbox.Content>
             <Checkbox.Control>
              <Checkbox.Indicator />
             </Checkbox.Control>
            </Checkbox.Content>
           </Checkbox>
          ) : null}
          <WindowMenuButton
           active={managing ? batch.has(entry.id) : entry.id === selected}
           title={entry.title || '未命名课次'}
           note={
            <span className="study-entry-note">
             <span>
              {entry.courseId ? courseName(courses, entry.courseId) : entry.weekly ? '未关联课程' : '未知课程'}
             </span>
             <span>
              {entry.weekly
               ? `${weekdayName(entry.weekday)} · ${periodName(entry)}${entry.location ? ` · ${entry.location}` : ''}`
               : entry.endsAt
                 ? formatTime(entry.endsAt, timezone)
                 : '时间待定'}
             </span>
             {entry.lectureId ? <span className="study-entry-linked">已关联</span> : null}
             {issueIds.has(entry.id) ? <span className="study-entry-warning">待完善</span> : null}
            </span>
           }
           onSelect={() => (managing ? toggleBatch(entry.id, !batch.has(entry.id)) : onSelect(entry.id))}
          >
           {entry.title || '未命名课次'}
          </WindowMenuButton>
         </div>
        ))}
       </section>
      ))}
      {!filtered.length ? <p className="study-menu__hint">没有匹配的课次。</p> : null}
     </div>
    ) : (
     <p className="study-menu__hint">还没有课表。上传课表图片、粘贴课表识别，或手动添加一节课。</p>
    )}
   </ScrollShadow>
   {confirmReplace ? (
    <ConfirmDialog
     title="替换当前课表草稿？"
     confirmLabel="继续识别"
     onCancel={() => setConfirmReplace(false)}
     onConfirm={() => {
      setConfirmReplace(false)
      setImporting(true)
     }}
    >
     当前有未保存的课表修改，识别成功后会用识别结果替换左侧草稿。
    </ConfirmDialog>
   ) : null}
   {confirmBatch ? (
    <ConfirmDialog
     title={`删除所选的 ${batch.size} 个课表条目？`}
     confirmLabel="删除所选"
     onCancel={() => setConfirmBatch(false)}
     onConfirm={() => {
      onDeleteMany([...batch])
      setBatch(new Set())
      setManaging(false)
      setConfirmBatch(false)
     }}
    >
     这些条目会从当前课表草稿移除，点击「保存课表」后才会写入；不会删除已关联的课次、聊天记录或教材划线。
    </ConfirmDialog>
   ) : null}
   {importing ? (
    <Modal title="识别文字课表" close={() => !busy && setImporting(false)}>
     <div className="study-import">
      <p className="study-menu__hint">
       请粘贴包含日期、结束时间和章节的课表；课程名称需要与已有课程匹配。缺少任一信息时不会猜测。
      </p>
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
        onClick={() =>
         void onParse(text, timezone).then(ok => {
          if (ok) {
           setImporting(false)
           setText('')
          }
         })
        }
       >
        开始识别
       </Button>
      </div>
     </div>
    </Modal>
   ) : null}
  </div>
 )
}

export function ScheduleContent(props: ScheduleProps) {
 const {entries, selected, busy, dirty, issues, onSave, onAdd, onDelete, onParseImage} = props
 const [confirmDelete, setConfirmDelete] = useState(false)
 const entry = entries.find(item => item.id === selected)
 if (!entries.length)
  return (
   <div className="study-empty">
    <h3>先准备课表</h3>
    <p>在左侧上传课表图片或粘贴课表识别，也可以手动添加一节课。课表条目会用来命名课次、关联教材划线。</p>
    <div className="study-empty__actions">
     <Button variant="primary" size="sm" isDisabled={busy} onClick={() => void onParseImage()}>
      上传课表图片
     </Button>
     <Button variant="tertiary" size="sm" isDisabled={busy || !props.courses.length} onClick={onAdd}>
      手动添加
     </Button>
    </div>
    {!props.courses.length ? <small>先在课程对话里创建一门课程。</small> : null}
   </div>
  )
 if (!entry)
  return (
   <div className="study-empty">
    <h3>从左侧选择一个课次</h3>
    <p>每个课次可以单独设置课程、章节、时间和关联的已有课次。</p>
   </div>
  )
 const entryIssues = issues.filter(issue => issue.id === entry.id)
 return (
  <>
   <ScheduleDetail key={entry.id} {...props} entry={entry} />
   <div className="study-danger-zone">
    <div>
     <strong>删除这个课表条目</strong>
     <small>只删除课表条目，不会删除课次、聊天记录或教材划线。</small>
    </div>
    <Button variant="danger-soft" size="sm" isDisabled={busy} onClick={() => setConfirmDelete(true)}>
     删除条目
    </Button>
   </div>
   {entryIssues.length ? (
    <div className="study-issue-list">
     {entryIssues.map(issue => (
      <small key={issue.message}>{issue.message}</small>
     ))}
    </div>
   ) : null}
   <div className="study-savebar">
    <div>
     <strong>{dirty ? '有未保存的修改' : '课表已保存'}</strong>
     <small>
      {(() => {
       if (issues.length) return `还有 ${issues.length} 处信息需要完善`
       if (dirty) return '保存后会同步更新已关联课次的名称和聊天标题'
       return '所有条目均已保存'
      })()}
     </small>
    </div>
    <Button variant="primary" size="sm" isDisabled={busy || !dirty || issues.length > 0} onClick={onSave}>
     保存课表
    </Button>
   </div>
   {confirmDelete ? (
    <ConfirmDialog
     title="删除课表条目？"
     busy={busy}
     onCancel={() => setConfirmDelete(false)}
     onConfirm={() => {
      setConfirmDelete(false)
      onDelete()
     }}
    >
     只删除这条课表配置，点击「保存课表」后才会写入。不会删除关联的课次、聊天记录或已经写入教材的划线。
    </ConfirmDialog>
   ) : null}
  </>
 )
}

function ScheduleDetail({entry, courses, detail, timezone, busy, onUpdate}: ScheduleProps & {entry: StudyEntry}) {
 const [date, setDate] = useState<CalendarDateTime | null>(() => dateTimeInZone(entry.endsAt, timezone))
 useEffect(() => {
  setDate(dateTimeInZone(entry.endsAt, timezone))
 }, [entry.id, entry.endsAt, timezone])
 const setDateTime = (next: CalendarDateTime | null) => {
  setDate(next)
  if (next) onUpdate({endsAt: isoFromDateTime(next, timezone)})
  else onUpdate({endsAt: ''})
 }
 const lectures = detail?.lectures || []
 const weekly = !!entry.weekly
 const summary = weekly
  ? [weekdayName(entry.weekday), periodName(entry), entry.location].filter(Boolean).join(' · ')
  : entry.chapter || '还没有填写教材章节'
 return (
  <div className="study-detail">
   <header className="study-detail__header">
    <div>
     <h3>{entry.title || '未命名课次'}</h3>
     <p>{summary}</p>
    </div>
   </header>
   <section className="study-form-section">
    <h4>基本信息</h4>
    <div className="study-form-grid">
     <StudyField label={weekly ? '课程（可选）' : '课程'}>
      <SelectField
       label="课程"
       searchable={courses.length > 8}
       clearable={weekly}
       placeholder={weekly ? '暂不关联' : '选择课程'}
       value={entry.courseId}
       onChange={next => onUpdate({courseId: next, lectureId: undefined, zhiyunCourseId: undefined, subId: undefined})}
       options={courses.map(course => ({value: course.id, label: course.name}))}
      />
     </StudyField>
     <StudyField label={weekly ? '课程名称' : '课次名称'}>
      <Input
       aria-label={weekly ? '课程名称' : '课次名称'}
       value={entry.title}
       onChange={event => onUpdate({title: event.target.value})}
       placeholder={weekly ? '例如：高等数学' : '例如：第 3 讲 极限'}
      />
     </StudyField>
     {!weekly ? (
      <StudyField label="教材章节" className="study-field--wide">
       <Input
        aria-label="教材章节"
        value={entry.chapter}
        onChange={event => onUpdate({chapter: event.target.value})}
        placeholder="例如：第二章 极限与连续"
       />
      </StudyField>
     ) : null}
    </div>
   </section>
   {weekly ? (
    <section className="study-form-section">
     <h4>每周安排</h4>
     <div className="study-form-grid">
      <WeeklyFields entry={entry} onUpdate={onUpdate} />
     </div>
    </section>
   ) : (
    <section className="study-form-section">
     <h4>时间</h4>
     <div className="study-form-grid">
      <StudyField
       label="课程结束时间"
       hint={`按 ${timezone || '课表时区'} 显示和保存`}
       error={validTimeZone(timezone) ? undefined : '课表时区无效，请回到识别课表里修改。'}
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
   <section className="study-form-section">
    <h4>关联与同步</h4>
    <div className="study-form-grid">
     <StudyField
      label="关联已有课次"
      className="study-field--wide"
      hint="生成教材划线前必须关联课次；保存课表会按这里的课次名称同步重命名。"
     >
      <SelectField
       label="关联已有课次"
       searchable={lectures.length > 8}
       clearable
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
    <ZhiyunSync entry={entry} courses={courses} busy={busy} onUpdate={onUpdate} />
    <Disclosure>
     <Disclosure.Heading>
      <Disclosure.Trigger>
       手动填写智云编号（备用）
       <Disclosure.Indicator />
      </Disclosure.Trigger>
     </Disclosure.Heading>
     <Disclosure.Content>
      <Disclosure.Body>
       <div className="study-form-grid">
        <StudyField label="智云课程编号">
         <Input
          aria-label="智云课程编号"
          inputMode="numeric"
          value={entry.zhiyunCourseId || ''}
          onChange={event => onUpdate({zhiyunCourseId: event.target.value || undefined})}
         />
        </StudyField>
        <StudyField label="智云课次编号">
         <Input
          aria-label="智云课次编号"
          inputMode="numeric"
          value={entry.subId || ''}
          onChange={event => onUpdate({subId: event.target.value || undefined})}
         />
        </StudyField>
       </div>
       <small>用于智云导入时匹配课次名称；只填数字。</small>
      </Disclosure.Body>
     </Disclosure.Content>
    </Disclosure>
   </section>
  </div>
 )
}
