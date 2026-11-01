import type {ReactNode} from 'react'

/** Saved rows retain action factories; busy belongs to the current render. */
export function TaskActions({actions, busy}: Readonly<{actions: (busy: boolean) => ReactNode; busy: boolean}>) {
 return <div className="task-row__actions">{actions(busy)}</div>
}
