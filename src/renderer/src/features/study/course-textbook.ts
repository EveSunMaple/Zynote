import type {StudyEntry} from '@shared/study'

export function sameTeachingCourse(a: StudyEntry, b: StudyEntry) {
 if (a.zhiyunCourseId || b.zhiyunCourseId) return a.zhiyunCourseId === b.zhiyunCourseId
 return !!a.courseId && a.courseId === b.courseId
}

export function setSessionTextbook(
 rows: StudyEntry[],
 sessionId: string,
 bookId: string,
 checked: boolean,
 savedBook?: string
) {
 return rows.map(row => {
  if (row.id !== sessionId) return row
  if (checked) return {...row, textbookId: bookId}
  if (row.textbookId !== bookId) return row
  // Toggling off a pending replacement restores that session's previously saved book.
  return {...row, textbookId: savedBook === bookId ? undefined : savedBook}
 })
}
