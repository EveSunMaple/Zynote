import type {ReactNode} from 'react'

export function StudyField({
 label,
 hint,
 error,
 children,
 className = ''
}: Readonly<{
 label: ReactNode
 hint?: ReactNode
 error?: ReactNode
 children: ReactNode
 className?: string
}>) {
 return (
  <div className={'study-field ' + className}>
   <span className="study-field__label">{label}</span>
   {children}
   {(() => {
    if (error) return <small className="study-field__error">{error}</small>
    if (hint) return <small>{hint}</small>
    return null
   })()}
  </div>
 )
}
