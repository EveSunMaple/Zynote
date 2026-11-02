import {z} from 'zod'
export const studyEntrySchema = z
 .object({
  id: z.string().uuid(),
  courseId: z.union([z.string().uuid(), z.literal('')]),
  lectureId: z.string().uuid().optional(),
  textbookId: z.string().uuid().optional(),
  title: z.string().trim().min(1).max(200),
  chapter: z.string().trim().max(500),
  endsAt: z.union([z.string().datetime({offset: true}), z.literal('')]),
  weekly: z.boolean().optional(),
  weekday: z.number().int().min(1).max(7).optional(),
  startPeriod: z.number().int().min(1).max(30).optional(),
  endPeriod: z.number().int().min(1).max(30).optional(),
  location: z.string().trim().max(200).optional(),
  timeText: z.string().trim().max(200).optional(),
  weekText: z.string().trim().max(200).optional(),
  skipZhiyunMatch: z.boolean().optional(),
  zhiyunCourseId: z.string().regex(/^\d+$/).optional(),
  zhiyunCourseLabel: z.string().optional(),
  subId: z.string().regex(/^\d+$/).optional(),
  zhiyunReplayLabel: z.string().optional()
 })
 .refine(entry => !!entry.weekly || (!!entry.courseId && !!entry.chapter), '请为课次选择课程并填写章节')
 .refine(entry => !entry.lectureId || !!entry.courseId, '关联课次前请先选择课程')
 .refine(entry => !entry.skipZhiyunMatch || (!entry.zhiyunCourseId && !entry.subId), '已跳过的课次不能同时关联智云')
 .refine(
  entry => entry.startPeriod === undefined || entry.endPeriod === undefined || entry.startPeriod <= entry.endPeriod,
  '结束节次不能早于开始节次'
 )
export type StudyEntry = z.infer<typeof studyEntrySchema>
export const checkAt = (endsAt: string) =>
 endsAt ? new Date(Date.parse(endsAt) + 4 * 60 * 60 * 1000).toISOString() : ''
export const highlightColors = {
 teacher: [0.76, 0.61, 0.44],
 focus: [1, 0.57, 0.57],
 personal: [0.99, 0.85, 0.45],
 ai: [0.55, 0.81, 0.68],
 ppt: [0.53, 0.76, 1],
 senior: [1, 0.66, 0.81]
} as const
export const highlightLabels = {
 focus: '老师重点',
 senior: '学长学姐重点',
 teacher: '老师讲授内容',
 ppt: 'PPT 内容',
 ai: 'AI 批注',
 personal: '我的笔记'
}
export const highlightSchema = z.object({
 id: z.string().uuid(),
 page: z.number().int().positive(),
 rect: z.tuple([z.number(), z.number(), z.number(), z.number()]),
 ink: z
  .array(z.tuple([z.number(), z.number()]))
  .min(2)
  .max(4096)
  .optional(),
 source: z.enum(['teacher', 'focus', 'ppt', 'senior', 'ai', 'personal']),
 quote: z.string().min(1).max(3000),
 evidence: z.string().min(1).max(5000),
 comments: z
  .array(
   z.object({
    kind: z.enum(['teacher', 'ai', 'personal']),
    text: z.string().min(1).max(5000),
    quote: z.string().max(3000).optional(),
    timestamp: z.string().max(100).optional()
   })
  )
  .max(100)
  .optional(),
 chapter: z
  .object({
   title: z.string().min(1).max(500),
   text: z.string().min(1).max(600),
   lastPage: z.number().int().positive(),
   refs: z
    .array(
     z.object({
      page: z.number().int().positive(),
      line: z.number().int().positive(),
      quote: z.string().min(1).max(500),
      rect: z.tuple([z.number(), z.number(), z.number(), z.number()])
     })
    )
    .min(1)
    .max(3)
  })
  .optional(),
 extension: z.boolean()
})
export type Highlight = z.infer<typeof highlightSchema>
export const highlightPriority = {focus: 0, senior: 1, teacher: 2, ppt: 3, ai: 4, personal: 5} as const
// Distinct sources may cover the same words.
export function prioritizedHighlights(rows: Highlight[]): Highlight[] {
 return [...rows].sort((a, b) => highlightPriority[a.source] - highlightPriority[b.source])
}
