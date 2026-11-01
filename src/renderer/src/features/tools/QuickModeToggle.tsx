import {Checkbox} from '@heroui/react'

// 快速模式：本次请求关闭模型的深度思考（仅通义千问生效），回答更快，复杂推理会弱一些。
export function QuickModeToggle({value, onChange}: Readonly<{value: boolean; onChange: (next: boolean) => void}>) {
 return (
  <Checkbox className="media-check" isSelected={value} onChange={onChange}>
   <Checkbox.Content>
    <Checkbox.Control>
     <Checkbox.Indicator />
    </Checkbox.Control>
    快速模式：关闭深度思考，回答更快（复杂推理会弱一些）
   </Checkbox.Content>
  </Checkbox>
 )
}
