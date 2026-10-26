package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/courseware"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
	"github.com/EveSunMaple/Zynote/backend/internal/zhiyun"
)

var zhiyunFilenameInvalid = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func (inv *Invoker) findOrCreateZhiyunLecture(
	write *storage.WriteDB,
	courseID, title, planned string,
) (map[string]any, error) {
	if planned != "" {
		if lecture, err := write.Lecture(planned); err == nil {
			return lecture, nil
		}
	}
	if lecture, err := write.LectureByTitle(courseID, title); err == nil && lecture != nil {
		return lecture, nil
	}
	return write.CreateLecture(courseID, title, "", "", "", "")
}

func sanitizeZhiyunFilename(value string) string {
	name := zhiyunFilenameInvalid.ReplaceAllString(value, "_")
	if strings.TrimSpace(name) == "" {
		name = "智云导出"
	}
	return name
}

func zhiyunFallbackError(err error) bool {
	message := err.Error()
	for _, marker := range []string{"暂无可导出", "未能生成PPT", "PPT生成超时", "文件格式暂不支持"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func (inv *Invoker) handleZhiyunExport(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Kind          string `json:"kind"`
		CourseID      string `json:"courseId"`
		SubID         string `json:"subId"`
		CourseName    string `json:"courseName"`
		LocalCourseID string `json:"localCourseId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || (input.Kind != "transcript" && input.Kind != "ppt") ||
		!zhiyunDigits.MatchString(input.CourseID) || !zhiyunDigits.MatchString(input.SubID) {
		return nil, contract.InputInvalid("智云导出参数无效")
	}
	return inv.runJob("zhiyun", func(ctx context.Context) (any, error) {
		client, err := inv.zhiyunClient(store)
		if err != nil {
			return nil, err
		}
		client.Progress = func(message string) {
			inv.emit(map[string]any{"kind": "progress", "data": map[string]any{
				"stage": "智云课堂", "message": message,
			}})
		}
		lessons, err := client.Lessons(ctx, input.CourseID)
		if err != nil {
			return nil, contract.Unavailable(err.Error())
		}
		var lesson *zhiyun.Lesson
		for index := range lessons {
			if lessons[index].ID == input.SubID {
				lesson = &lessons[index]
				break
			}
		}
		if lesson == nil {
			return nil, contract.InputInvalid("没有找到所选课次")
		}
		write, err := openWriteDB(state)
		if err != nil {
			return nil, err
		}
		defer write.Close()
		var course map[string]any
		if input.LocalCourseID != "" {
			if !config.IsUUID(input.LocalCourseID) {
				return nil, contract.InputInvalid("课程标识无效")
			}
			course, err = write.Course(input.LocalCourseID)
			if err != nil {
				return nil, err
			}
		} else {
			name := strings.TrimSpace(input.CourseName)
			if name == "" {
				name = "智云课程 " + input.CourseID
			}
			if len([]rune(name)) < 1 || len([]rune(name)) > 200 {
				return nil, contract.InputInvalid("课程名称无效")
			}
			existing, _ := write.CourseByName(name)
			if existing != nil {
				course = existing
			} else {
				course, err = write.CreateCourse(name)
				if err != nil {
					return nil, err
				}
			}
		}
		courseID := storage.ToStringValue(course["id"])
		courseName := storage.ToStringValue(course["name"])
		if err := ensureCourseLibrary(state.LibraryRoot, []string{courseName}); err != nil {
			return nil, err
		}
		title := lesson.Title
		plannedLecture := ""
		readDB, err := openReadOnlyDB(state)
		if err == nil {
			if rows, err := storage.QueryAll(readDB, "SELECT json FROM study_entries"); err == nil {
				for _, row := range rows {
					var entry map[string]any
					if json.Unmarshal([]byte(storage.ToStringValue(row["json"])), &entry) != nil {
						continue
					}
					if textValue(entry["courseId"]) == courseID && textValue(entry["zhiyunCourseId"]) == input.CourseID &&
						textValue(entry["subId"]) == input.SubID {
						if value := textValue(entry["title"]); value != "" {
							title = value
						}
						plannedLecture = textValue(entry["lectureId"])
						break
					}
				}
			}
			readDB.Close()
		}
		lecture, err := inv.findOrCreateZhiyunLecture(write, courseID, title, plannedLecture)
		if err != nil {
			return nil, err
		}
		lectureID := storage.ToStringValue(lecture["id"])
		if input.Kind == "transcript" {
			directory, err := library.CourseDirectory(state.LibraryRoot, "Transcripts", courseName)
			if err != nil {
				return nil, err
			}
			path, err := library.ReserveFile(directory, sanitizeZhiyunFilename(lesson.Title)+".docx")
			if err != nil {
				return nil, err
			}
			resource, err := client.Resource(ctx, input.CourseID, input.SubID, "transcript")
			if err != nil {
				_ = os.Remove(path)
				return nil, contract.Unavailable(err.Error())
			}
			if err := client.Download(ctx, resource.URL, path); err != nil {
				_ = os.Remove(path)
				return nil, contract.Unavailable(err.Error())
			}
			transcript, err := documents.ReadTranscript(inv.native, path)
			if err != nil {
				_ = os.Remove(path)
				return nil, err
			}
			if _, err := write.SetLectureTranscript(lectureID, path, transcript.Text, boolToInt(transcript.Estimated)); err != nil {
				_ = os.Remove(path)
				return nil, err
			}
			_ = write.DeleteCorrectionCheckpoint(lectureID)
			return map[string]any{"path": path, "lectureId": lectureID, "courseId": courseID}, nil
		}
		pptDirectory, err := library.CourseDirectory(state.LibraryRoot, "PPT", courseName)
		if err != nil {
			return nil, err
		}
		resource, resourceErr := client.Resource(ctx, input.CourseID, input.SubID, "ppt")
		var downloaded string
		if resourceErr == nil {
			downloaded, err = library.ReserveFile(pptDirectory, sanitizeZhiyunFilename(resource.Filename))
			if err != nil {
				return nil, err
			}
			if err := client.Download(ctx, resource.URL, downloaded); err != nil {
				_ = os.Remove(downloaded)
				return nil, contract.Unavailable(err.Error())
			}
			if strings.ToLower(filepath.Ext(downloaded)) == ".pptx" {
				parsed, parseErr := documents.ParseCourseware(inv.native, downloaded)
				if parseErr == nil && strings.TrimSpace(parsed.Text) != "" {
					serialized, err := courseware.SerializeSourceCache(
						downloaded, parsed.Text, "local", parsed.NeedsOcr, parsed.Omissions,
					)
					if err != nil {
						_ = os.Remove(downloaded)
						return nil, err
					}
					if _, err := write.SetLectureCourseware(lectureID, downloaded, serialized); err != nil {
						return nil, err
					}
					_ = write.DeleteCorrectionCheckpoint(lectureID)
					return map[string]any{"path": downloaded, "lectureId": lectureID, "courseId": courseID}, nil
				}
			}
		} else if !zhiyunFallbackError(resourceErr) {
			return nil, contract.Unavailable(resourceErr.Error())
		}
		videoURL, _ := client.LessonVideoURL(ctx, input.CourseID, input.SubID)
		if videoURL == "" {
			return nil, contract.InputInvalid("PPT 提取失败，且本课次暂无录像；已下载文件保留")
		}
		inv.videoProgress("智云课件无法读取，正在改用课程录像")
		task, err := videoPptService(state).CreateBlankTask(title, nil)
		if err != nil {
			return nil, videoPptError(err)
		}
		if err := client.Download(ctx, videoURL, task.Path); err != nil {
			return nil, contract.Unavailable(err.Error())
		}
		chat, err := inv.videoChat(store, state)
		if err != nil {
			return nil, err
		}
		prepared, err := videoProcessor(state).Prepare(ctx, task, chat, inv.videoProgress)
		if err != nil {
			return nil, videoPptError(err)
		}
		if prepared.Source.Warning != "" {
			return nil, contract.InputInvalid(prepared.Source.Warning + " 视频已保留，可在视频提取页继续检查")
		}
		review, err := videoProcessor(state).Extract(ctx, task, prepared.Source.Crop, defaultVideoPrompt, chat, inv.videoProgress)
		if err != nil {
			return nil, videoPptError(err)
		}
		if review.Warning != "" {
			return nil, contract.InputInvalid(review.Warning + " 视频已保留，可继续检查")
		}
		choices := make([]videoppt.Choice, 0, len(review.Pages))
		for _, page := range review.Pages {
			choices = append(choices, videoppt.Choice{Page: page.Page, Keep: page.Keep, Crop: page.Crop})
		}
		result, _, err := videoProcessor(state).Export(ctx, task, choices, pptDirectory)
		if err != nil {
			return nil, videoPptError(err)
		}
		parsed, err := documents.ParseCourseware(inv.native, result.Path)
		if err != nil {
			return nil, err
		}
		serialized, err := courseware.SerializeSourceCache(
			result.Path, parsed.Text, "local", parsed.NeedsOcr, parsed.Omissions,
		)
		if err != nil {
			return nil, err
		}
		if _, err := write.SetLectureCourseware(lectureID, result.Path, serialized); err != nil {
			return nil, err
		}
		_ = write.DeleteCorrectionCheckpoint(lectureID)
		if downloaded != "" {
			_ = os.Remove(downloaded)
		}
		if err := videoPptService(state).DiscardCompleted(task.ID); err != nil {
			inv.videoProgress("PDF 已保存，临时文件清理失败：" + err.Error())
		}
		return map[string]any{"path": result.Path, "lectureId": lectureID, "courseId": courseID, "fromVideo": true}, nil
	})
}
