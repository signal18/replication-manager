package archiver

import "google.golang.org/protobuf/runtime/protoimpl"

type BackupStat struct {
	TotalSize      int64 `protobuf:"varint,1,opt,name=total_size,proto3" json:"total_size"`
	TotalFileCount int64 `protobuf:"varint,2,opt,name=total_file_count,proto3" json:"total_file_count"`
	TotalBlobCount int64 `protobuf:"varint,3,opt,name=total_blob_count,proto3" json:"total_blob_count"`
}

type Backup struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Id       string   `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	ShortId  string   `protobuf:"bytes,2,opt,name=short_id,proto3" json:"short_id,omitempty"`
	Time     string   `protobuf:"bytes,3,opt,name=time,proto3" json:"time,omitempty"`
	Tree     string   `protobuf:"bytes,4,opt,name=tree,proto3" json:"tree,omitempty"`
	Paths    []string `protobuf:"bytes,5,rep,name=paths,proto3" json:"paths,omitempty"`
	Hostname string   `protobuf:"bytes,6,opt,name=hostname,proto3" json:"hostname,omitempty"`
	Username string   `protobuf:"bytes,7,opt,name=username,proto3" json:"username,omitempty"`
	Uid      int64    `protobuf:"varint,8,opt,name=uid,proto3" json:"uid,omitempty"`
	Gid      int64    `protobuf:"varint,9,opt,name=gid,proto3" json:"gid,omitempty"`
	Tags     []string `protobuf:"bytes,10,rep,name=tags,proto3" json:"tags,omitempty"`
}
