import {useEffect, useState, type ReactNode} from 'react'
import {Button} from '@heroui/react'
import type {Highlight} from '@shared/study'
import {errorMessage} from '@renderer/ui/ToastHost'
import '@renderer/features/study/teacher-annotations.css'

export type TeacherMark = Pick<Highlight, 'id' | 'page' | 'rect' | 'quote'> & {
 comments: NonNullable<Highlight['comments']>
}
type TeacherPage = {items: TeacherMark[]; total: number; replaySubId?: string}
type ListProps = {
 bookId: string
 lectureId?: string
 onLocate: (row: TeacherMark) => void
 actions: (replaySubId?: string) => ReactNode
}

export function TeacherAnnotations({version, ...props}: Readonly<ListProps & {version: number}>) {
 // A newly opened or rewritten book starts at the first result with fresh page anchors.
 return <SavedTeacherList key={[props.bookId, props.lectureId, version].join(':')} {...props} />
}

function SavedTeacherList({bookId, lectureId, onLocate, actions}: Readonly<ListProps>) {
 const [data, setData] = useState<TeacherPage | null>(null)
 const [request, setRequest] = useState({offset: 0})
 const [pending, setPending] = useState(true)
 const [error, setError] = useState('')
 useEffect(() => {
  let active = true
  setPending(true)
  setError('')
  void window.assistant
   .invoke<TeacherPage>('textbook:annotations', {id: bookId, kind: 'teacher', offset: request.offset, lectureId})
   .then(page => {
    if (active)
     setData(previous => ({
      ...page,
      items: request.offset && previous ? [...previous.items, ...page.items] : page.items
     }))
   })
   .catch(reason => {
    if (active) setError(errorMessage(reason))
   })
   .finally(() => {
    if (active) setPending(false)
   })
  return () => {
   active = false
  }
 }, [bookId, lectureId, request])
 return (
  <section className="teacher-annotations" aria-label="全书已保存的老师批注" aria-busy={pending}>
   {data && data.items.length > 0 && (
    <>
     <p className="study-menu__hint">智云原话 · 全书共 {data.total} 处，点一条定位到正文</p>
     <ul className="teacher-annotations__list">
      {data.items.map(row => (
       <li key={row.id}>
        <Button fullWidth variant="ghost" className="teacher-annotations__entry" onPress={() => onLocate(row)}>
         <span className="teacher-annotations__page">教材第 {row.page} 页</span>
         {row.comments.map(comment => (
          <span className="teacher-annotations__quote" key={JSON.stringify([comment.timestamp, comment.quote])}>
           {comment.timestamp && <time className="teacher-annotations__time">{comment.timestamp}</time>}
           {comment.quote}
          </span>
         ))}
         {row.quote && (
          <span className="teacher-annotations__text" title={row.quote}>
           教材 · {row.quote}
          </span>
         )}
        </Button>
       </li>
      ))}
     </ul>
    </>
   )}
   {pending ? (
    <p className="study-menu__hint" role="status">
     正在读取老师批注…
    </p>
   ) : error ? (
    <div className="textbook-next">
     <p className="study-inline-error" role="alert">
      读取老师批注失败。{error}
     </p>
     <Button variant="ghost" size="sm" onPress={() => setRequest({...request})}>
      重新读取批注
     </Button>
    </div>
   ) : data?.total === 0 ? (
    <p className="study-menu__hint">这本教材还没有已保存的老师批注。</p>
   ) : (
    data &&
    data.items.length < data.total && (
     <Button variant="ghost" size="sm" onPress={() => setRequest({offset: data.items.length})}>
      查看更多老师批注
     </Button>
    )
   )}
   {data && actions(data.replaySubId)}
  </section>
 )
}
