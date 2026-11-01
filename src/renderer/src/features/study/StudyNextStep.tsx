import type {ReactNode} from 'react'

export function StudyNextStep({children}: Readonly<{children: ReactNode}>) {
 return (
  <div className="study-next-step" aria-live="polite" aria-atomic="true">
   <span className="study-next-step__label">下一步</span>
   <span>{children}</span>
  </div>
 )
}
