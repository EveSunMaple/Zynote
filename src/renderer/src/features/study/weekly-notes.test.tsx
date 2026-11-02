import {expect, it} from 'vitest'
import {renderToStaticMarkup} from 'react-dom/server'
import {studyEntrySchema, type StudyEntry} from '@shared/study'
import {autoReplay} from './ZhiyunSync'
import {WeeklySchedule} from './WeeklySchedule'
const entry: StudyEntry = {
 id: '11111111-1111-4111-8111-111111111111',
 courseId: '',
 title: '代数',
 chapter: '',
 endsAt: '',
 weekly: true,
 weekday: 1,
 startPeriod: 1,
 endPeriod: 2,
 timeText: '08:00–09:40',
 weekText: '1-16周（双周）'
}
it('retains visible time/week notes in schema and weekly display', () => {
 expect(studyEntrySchema.parse(entry).weekText).toBe(entry.weekText)
 const html = renderToStaticMarkup(<WeeklySchedule entries={[entry]} selected={entry.id} onSelect={() => {}} />)
 expect(html).toContain(entry.timeText)
 expect(html).toContain(entry.weekText)
 for (const value of [123, null, '周'.repeat(201)])
  expect(studyEntrySchema.safeParse({...entry, weekText: value}).success).toBe(false)
})
it('never auto-selects a replay when week conditions need confirmation', () => {
 const lessons = [{id: '1', title: '2026年11月2日 第1-2节', teacher: '', videoAvailable: true}]
 expect(autoReplay(entry, lessons)).toBeUndefined()
 expect(autoReplay({...entry, weekText: ''}, lessons)?.id).toBe('1')
 expect(autoReplay({...entry, weekText: undefined}, lessons)?.id).toBe('1')
})
