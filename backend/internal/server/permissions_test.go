package server

import "testing"

func TestRoomPermissions(t *testing.T) {
	if !CanManage(42, 42) || CanManage(42, 43) {
		t.Fatal("owner permission invalid")
	}
	members := []Member{{UserID: 42}}
	if !isMember(members, 42) || isMember(members, 43) {
		t.Fatal("membership invalid")
	}
}
