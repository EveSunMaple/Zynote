import {Button, Disclosure, Input} from '@heroui/react'
import {StudyField} from '@renderer/features/study/Field'
import {SelectField} from '@renderer/ui/SelectField'
import type {StudyEntry} from '@shared/study'

const weekdays = ['周一', '周二', '周三', '周四', '周五', '周六', '周日']
const dayOptions = weekdays.map((label, index) => ({value: String(index + 1), label}))
const periodOptions = Array.from({length: 30}, (_, index) => ({value: String(index + 1), label: `第 ${index + 1} 节`}))

export function weekdayName(day?: number) {
 return weekdays[(day || 0) - 1] || '星期待核对'
}

export function periodName(entry: StudyEntry) {
 const {startPeriod, endPeriod} = entry
 if (!startPeriod && !endPeriod) return '节次待核对'
 if (startPeriod === endPeriod) return `第 ${startPeriod} 节`
 return `第 ${startPeriod ?? '?'}–${endPeriod ?? '?'} 节`
}

export function WeeklyFields({
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
     placeholder="待核对"
     value={entry.location ?? ''}
     maxLength={200}
     onChange={event => onUpdate({location: event.target.value})}
    />
   </StudyField>
   <StudyField label="时刻（原图）" hint="只保留可见原文，不按节次猜测钟点；请对照原图核对。">
    <Input
     aria-label="时刻（原图）"
     value={entry.timeText ?? ''}
     maxLength={200}
     onChange={event => onUpdate({timeText: event.target.value})}
    />
   </StudyField>
   <StudyField label="周次与单双周（原图）" hint="仅作核对提示，不自动换算日期；存在限制时请手动选择智云回放。">
    <Input
     aria-label="周次与单双周（原图）"
     value={entry.weekText ?? ''}
     maxLength={200}
     onChange={event => onUpdate({weekText: event.target.value})}
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

function isPlaced(entry: StudyEntry) {
 return (
  !!entry.weekday &&
  entry.weekday >= 1 &&
  entry.weekday <= 7 &&
  !!entry.startPeriod &&
  !!entry.endPeriod &&
  entry.startPeriod >= 1 &&
  entry.startPeriod <= entry.endPeriod &&
  entry.endPeriod <= 30
 )
}

// Merge adjacent cells only while they contain the same classes, so overlapping classes stay selectable.
function dayCells(entries: StudyEntry[], count: number) {
 const cells: ({rows: StudyEntry[]; span: number} | null)[] = []
 for (let period = 1; period <= count; period++) {
  const rows = entries.filter(entry => period >= (entry.startPeriod ?? 0) && period <= (entry.endPeriod ?? 0))
  const first = cells.findLast(cell => cell !== null)
  if (rows.length && first?.rows.length === rows.length && rows.every((row, index) => row === first.rows[index])) {
   first.span++
   cells.push(null)
  } else cells.push({rows, span: 1})
 }
 return cells
}

export function WeeklySchedule({
 entries,
 selected,
 onSelect
}: Readonly<{
 entries: StudyEntry[]
 selected: string
 onSelect: (id: string) => void
}>) {
 const weekly = entries.filter(entry => entry.weekly)
 if (!weekly.length) return null
 const placed = weekly.filter(isPlaced)
 const pending = weekly.filter(entry => !isPlaced(entry))
 const count = Math.max(1, ...placed.map(entry => entry.endPeriod ?? 1))
 const columns = weekdays.map((_, day) =>
  dayCells(
   placed.filter(entry => entry.weekday === day + 1),
   count
  )
 )
 return (
  <Disclosure className="study-week" defaultExpanded>
   <Disclosure.Heading>
    <Disclosure.Trigger className="study-fold__trigger study-week__heading">
     周课表
     <Disclosure.Indicator />
    </Disclosure.Trigger>
   </Disclosure.Heading>
   <Disclosure.Content>
    <Disclosure.Body className="study-week__body">
     {placed.length > 0 && (
      <div className="study-week__scroll">
       <table className="study-week__table" aria-label="每周课程安排">
        <thead>
         <tr>
          <th scope="col" className="study-week__period">
           节次
          </th>
          {weekdays.map(day => (
           <th key={day} scope="col" className="study-week__day">
            {day}
           </th>
          ))}
         </tr>
        </thead>
        <tbody>
         {Array.from({length: count}, (_, index) => (
          <tr key={String(index + 1)}>
           <th scope="row" className="study-week__period">
            {index + 1}
           </th>
           {columns.map((cells, day) => {
            const cell = cells[index]
            if (!cell) return null
            return (
             <td key={weekdays[day]} rowSpan={cell.span} className="study-week__cell">
              {cell.rows.map(entry => (
               <Button
                key={entry.id}
                size="sm"
                variant="ghost"
                aria-pressed={selected === entry.id}
                aria-label={`${weekdays[day]} ${periodName(entry)} ${entry.title || '课程名待核对'} ${entry.location || ''}`}
                className="study-week__course"
                onPress={() => onSelect(entry.id)}
               >
                <span>{entry.title || '课程名待核对'}</span>
                {entry.location && <small className="study-week__location">{entry.location}</small>}
                {entry.timeText && <small className="study-week__location">{entry.timeText}</small>}
                {entry.weekText && <small className="study-week__location">{entry.weekText}</small>}
               </Button>
              ))}
             </td>
            )
           })}
          </tr>
         ))}
        </tbody>
       </table>
      </div>
     )}
     {pending.length > 0 && (
      <div className="study-week__pending">
       <strong>待排课</strong>
       {pending.map(entry => (
        <Button
         key={entry.id}
         size="sm"
         variant={entry.id === selected ? 'secondary' : 'tertiary'}
         onPress={() => onSelect(entry.id)}
        >
         {entry.title || '课程名待核对'} · {weekdayName(entry.weekday)} · {periodName(entry)}
        </Button>
       ))}
      </div>
     )}
    </Disclosure.Body>
   </Disclosure.Content>
  </Disclosure>
 )
}
