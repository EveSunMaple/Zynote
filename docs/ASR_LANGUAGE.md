# 离线字幕语言与恢复身份

字幕页可为新建离线转写选择中文（默认）、英语或自动检测。三个入口为本地视频、远程视频和分集批量字幕。优先取得站内字幕的行为保持；语言选择不会强制重转已有站内字幕。

API 的可选 `asrLanguage` 接受 `zh`、`en`、`auto`；空串、null、缺省的新任务均为中文。大小写、空白和其它语言不被悄悄纠正。恢复时省略字段沿用任务绑定值，显式不同值返回输入冲突；改变语言应新建 operationId。中文的存储省略字段，保持旧任务身份兼容。

语言绑定在 operation、字幕 task、batch 及子任务中；保存、恢复和 checkpoint 校验使用同一语言。旧 V1 ASR checkpoint 仅作为中文恢复；非中文必须具有匹配 V2 ASR provenance，或 URL、分集、内容 SHA 与 prepared 状态均验证通过的站内字幕 checkpoint。不会为站内字幕伪造 ASR 来源。

自动检测发生在现有 native 每次 VAD 范围转写的首个窗口，不保证中英混讲更准确。此次没有更改 native 解码器、模型、VAD、字幕清洗或用户原文。

## 有界模型诊断（2026-11-01）

使用 CAiRE/ASCEND 的固定公开版本 `737e9800ae31be9932ba8464c80366559bd28424`（CC-BY-SA-4.0，Lovenia 等，LREC 2022，https://aclanthology.org/2022.lrec-1.788/），预先选择 4 位说话者的 8 条完整自然语音，共 54.09 秒，最长 12.16 秒；参考为发布者 expert-generated 文本。跨 train/validation/test 选样只作诊断，不作为官方测试集或长课验收。

Small 的 zh/auto 共 16 次请求中 14 次通过严格结构门禁；同一混讲片段两种语言均存在 10ms 相邻字幕重叠，native 正常返回但不计准确率。Medium 的 8 次 zh 请求均通过。仅在共同有效样本配对：中文 4 条 CER 为 Small 9/54、Medium 4/54；混讲 3 条 MER 为 Small 44/127、Medium 51/127。繁简归一仅作副口径：中文两者 1/54，混讲分别 23/127、27/127。Small zh 与 auto 在此共同集合相同。不得将缺测当零错误。

候选为社区 ONNX 转换 `onnx-community/whisper-medium_timestamped@9fe933aaa6b0244316eb961f36d33dfedf0c0b0c`，模型卡标注 MIT；非 OpenAI 官方导出。80-mel 图可以通过现有 native 执行，但混讲退化、冷启动与内存增加，因此不替换默认 Small，也不提交权重或语料。未验证 >30 秒连续课堂、真实用户音频、独立英文语料或不同硬件。
