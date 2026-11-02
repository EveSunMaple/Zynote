import type {ReactNode} from 'react'

export function StudyNextStep({children}: Readonly<{children: ReactNode}>) {
 return (
  <div className="study-next-step" aria-live="polite" aria-atomic="true">
   <span>{children}</span>
  </div>
 )
}
