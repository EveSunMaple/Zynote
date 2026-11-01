import {Checkbox, Disclosure} from '@heroui/react'
import {formatDateTime} from '@renderer/features/study/datetime'
import {periodName, weekdayName} from '@renderer/features/study/WeeklySchedule'
import type {StudyEntry} from '@shared/study'

export function sessionDescription(row: StudyEntry, timezone: string) {
 const time = row.weekly ? `${weekdayName(row.weekday)} · ${periodName(row)}` : formatDateTime(row.endsAt, timezone)
 return [time, !row.weekly && row.chapter, row.zhiyunCourseLabel].filter(Boolean).join(' · ')
}

export function TextbookSessions({
 entries,
 bookId,
 currentTitle,
 timezone,
 busy,
 onLink
}: Readonly<{
 entries: StudyEntry[]
 bookId: string
 currentTitle?: string
 timezone: string
 busy: boolean
 onLink: (id: string, checked: boolean) => void
}>) {
 const count = entries.filter(row => row.textbookId === bookId).length
 const ordered = [...entries].sort(
  (a, b) =>
   Number(b.title === currentTitle) - Number(a.title === currentTitle) ||
   a.title.localeCompare(b.title, 'zh-CN') ||
   (a.weekday ?? 8) - (b.weekday ?? 8) ||
   (a.startPeriod ?? 0) - (b.startPeriod ?? 0) ||
   a.endsAt.localeCompare(b.endsAt)
 )
 return (
  <Disclosure className="study-fold">
   <Disclosure.Heading>
    <Disclosure.Trigger className="study-fold__trigger">
     关联课次（{count}）<Disclosure.Indicator />
    </Disclosure.Trigger>
   </Disclosure.Heading>
   <Disclosure.Content>
    <Disclosure.Body className="study-fold__body">
     {ordered.length ? (
      <div role="group" aria-label="选择使用本教材的课次" className="textbook-session-list">
       {ordered.map(row => {
        const title = row.title.trim() || '未命名课次'
        const description = sessionDescription(row, timezone)
        return (
         <Checkbox
          key={row.id}
          isDisabled={busy}
          isSelected={row.textbookId === bookId}
          aria-label={[title, description].filter(Boolean).join(' · ')}
          onChange={checked => onLink(row.id, checked)}
         >
          <Checkbox.Content>
           <Checkbox.Control>
            <Checkbox.Indicator />
           </Checkbox.Control>
           <span className="search-select-option">
            <span>{title}</span>
            {description && <small>{description}</small>}
            {row.textbookId && row.textbookId !== bookId && <small>已用其他教材，勾选将替换</small>}
           </span>
          </Checkbox.Content>
         </Checkbox>
        )
       })}
      </div>
     ) : (
      <p className="study-menu__hint">先在课表中添加课次，再选择哪些课次使用这本教材。</p>
     )}
    </Disclosure.Body>
   </Disclosure.Content>
  </Disclosure>
 )
}
