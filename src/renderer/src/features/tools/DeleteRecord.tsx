import {Button} from '@heroui/react'
import {useState} from 'react'

// 两步删除：第一次点击只切换成“确认删除”，再点一次才真正删除；查看器里没有确认弹窗可用。
export function DeleteRecord({
 label,
 isDisabled,
 onDelete
}: Readonly<{label: string; isDisabled?: boolean; onDelete: () => void}>) {
 const [armed, setArmed] = useState(false)
 if (!armed)
  return (
   <Button variant="ghost" size="sm" isDisabled={isDisabled} onClick={() => setArmed(true)}>
    {label}
   </Button>
  )
 return (
  <span className="row">
   <Button variant="danger" size="sm" isDisabled={isDisabled} onClick={onDelete}>
    确认删除
   </Button>
   <Button variant="ghost" size="sm" onClick={() => setArmed(false)}>
    取消
   </Button>
  </span>
 )
}
