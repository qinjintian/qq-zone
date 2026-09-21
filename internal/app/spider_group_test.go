// 群相册备份任务识别单元测试

package app

import "testing"

func TestIsGroupAlbumTask(t *testing.T) {
	if IsGroupAlbumTask(nil) {
		t.Fatal("nil")
	}
	if !IsGroupAlbumTask(&TaskRecord{Mode: TaskModeGroupAlbum}) {
		t.Fatal("mode")
	}
	if !IsGroupAlbumTask(&TaskRecord{Mode: TaskModeRetryFailed, GroupID: "123456"}) {
		t.Fatal("group id")
	}
	retry := &TaskRecord{
		Mode: TaskModeRetryFailed,
		OpenFailedItems: []FailedItem{{
			Kind:    FailedKindGroup,
			GroupID: "123456",
		}},
	}
	if !IsGroupAlbumTask(retry) {
		t.Fatal("retry kind")
	}
	if IsGroupAlbumTask(&TaskRecord{Mode: TaskModeShuoShuo}) {
		t.Fatal("shuoshuo")
	}
	if IsGroupAlbumTask(&TaskRecord{Mode: TaskModeBackup}) {
		t.Fatal("personal album")
	}
}
