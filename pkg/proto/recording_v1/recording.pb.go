// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.3
// 	protoc        v4.25.3
// source: recording.proto

package recording

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type SaveRecordingRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	UserId        string                 `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`                // ID пользователя
	FileName      string                 `protobuf:"bytes,2,opt,name=file_name,json=fileName,proto3" json:"file_name,omitempty"`          // Имя файла записи
	FilePath      string                 `protobuf:"bytes,3,opt,name=file_path,json=filePath,proto3" json:"file_path,omitempty"`          // Путь к файлу с записью
	SizeBytes     int64                  `protobuf:"varint,4,opt,name=size_bytes,json=sizeBytes,proto3" json:"size_bytes,omitempty"`      // Размер файла
	MimeType      string                 `protobuf:"bytes,5,opt,name=mime_type,json=mimeType,proto3" json:"mime_type,omitempty"`          // MIME тип файла
	RecordingId   string                 `protobuf:"bytes,6,opt,name=recording_id,json=recordingId,proto3" json:"recording_id,omitempty"` // ID записи (egress_id)
	RoomId        string                 `protobuf:"bytes,7,opt,name=room_id,json=roomId,proto3" json:"room_id,omitempty"`                // ID комнаты
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *SaveRecordingRequest) Reset() {
	*x = SaveRecordingRequest{}
	mi := &file_recording_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SaveRecordingRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SaveRecordingRequest) ProtoMessage() {}

func (x *SaveRecordingRequest) ProtoReflect() protoreflect.Message {
	mi := &file_recording_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SaveRecordingRequest.ProtoReflect.Descriptor instead.
func (*SaveRecordingRequest) Descriptor() ([]byte, []int) {
	return file_recording_proto_rawDescGZIP(), []int{0}
}

func (x *SaveRecordingRequest) GetUserId() string {
	if x != nil {
		return x.UserId
	}
	return ""
}

func (x *SaveRecordingRequest) GetFileName() string {
	if x != nil {
		return x.FileName
	}
	return ""
}

func (x *SaveRecordingRequest) GetFilePath() string {
	if x != nil {
		return x.FilePath
	}
	return ""
}

func (x *SaveRecordingRequest) GetSizeBytes() int64 {
	if x != nil {
		return x.SizeBytes
	}
	return 0
}

func (x *SaveRecordingRequest) GetMimeType() string {
	if x != nil {
		return x.MimeType
	}
	return ""
}

func (x *SaveRecordingRequest) GetRecordingId() string {
	if x != nil {
		return x.RecordingId
	}
	return ""
}

func (x *SaveRecordingRequest) GetRoomId() string {
	if x != nil {
		return x.RoomId
	}
	return ""
}

type SaveRecordingResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	FileId        string                 `protobuf:"bytes,1,opt,name=file_id,json=fileId,proto3" json:"file_id,omitempty"`       // UUID сохраненного файла
	FolderId      string                 `protobuf:"bytes,2,opt,name=folder_id,json=folderId,proto3" json:"folder_id,omitempty"` // ID папки "Записи"
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *SaveRecordingResponse) Reset() {
	*x = SaveRecordingResponse{}
	mi := &file_recording_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SaveRecordingResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SaveRecordingResponse) ProtoMessage() {}

func (x *SaveRecordingResponse) ProtoReflect() protoreflect.Message {
	mi := &file_recording_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SaveRecordingResponse.ProtoReflect.Descriptor instead.
func (*SaveRecordingResponse) Descriptor() ([]byte, []int) {
	return file_recording_proto_rawDescGZIP(), []int{1}
}

func (x *SaveRecordingResponse) GetFileId() string {
	if x != nil {
		return x.FileId
	}
	return ""
}

func (x *SaveRecordingResponse) GetFolderId() string {
	if x != nil {
		return x.FolderId
	}
	return ""
}

var File_recording_proto protoreflect.FileDescriptor

var file_recording_proto_rawDesc = []byte{
	0x0a, 0x0f, 0x72, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x69, 0x6e, 0x67, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x12, 0x09, 0x72, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x69, 0x6e, 0x67, 0x22, 0xe1, 0x01, 0x0a,
	0x14, 0x53, 0x61, 0x76, 0x65, 0x52, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x69, 0x6e, 0x67, 0x52, 0x65,
	0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x17, 0x0a, 0x07, 0x75, 0x73, 0x65, 0x72, 0x5f, 0x69, 0x64,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x75, 0x73, 0x65, 0x72, 0x49, 0x64, 0x12, 0x1b,
	0x0a, 0x09, 0x66, 0x69, 0x6c, 0x65, 0x5f, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x08, 0x66, 0x69, 0x6c, 0x65, 0x4e, 0x61, 0x6d, 0x65, 0x12, 0x1b, 0x0a, 0x09, 0x66,
	0x69, 0x6c, 0x65, 0x5f, 0x70, 0x61, 0x74, 0x68, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08,
	0x66, 0x69, 0x6c, 0x65, 0x50, 0x61, 0x74, 0x68, 0x12, 0x1d, 0x0a, 0x0a, 0x73, 0x69, 0x7a, 0x65,
	0x5f, 0x62, 0x79, 0x74, 0x65, 0x73, 0x18, 0x04, 0x20, 0x01, 0x28, 0x03, 0x52, 0x09, 0x73, 0x69,
	0x7a, 0x65, 0x42, 0x79, 0x74, 0x65, 0x73, 0x12, 0x1b, 0x0a, 0x09, 0x6d, 0x69, 0x6d, 0x65, 0x5f,
	0x74, 0x79, 0x70, 0x65, 0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08, 0x6d, 0x69, 0x6d, 0x65,
	0x54, 0x79, 0x70, 0x65, 0x12, 0x21, 0x0a, 0x0c, 0x72, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x69, 0x6e,
	0x67, 0x5f, 0x69, 0x64, 0x18, 0x06, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x72, 0x65, 0x63, 0x6f,
	0x72, 0x64, 0x69, 0x6e, 0x67, 0x49, 0x64, 0x12, 0x17, 0x0a, 0x07, 0x72, 0x6f, 0x6f, 0x6d, 0x5f,
	0x69, 0x64, 0x18, 0x07, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x72, 0x6f, 0x6f, 0x6d, 0x49, 0x64,
	0x22, 0x4d, 0x0a, 0x15, 0x53, 0x61, 0x76, 0x65, 0x52, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x69, 0x6e,
	0x67, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x17, 0x0a, 0x07, 0x66, 0x69, 0x6c,
	0x65, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x66, 0x69, 0x6c, 0x65,
	0x49, 0x64, 0x12, 0x1b, 0x0a, 0x09, 0x66, 0x6f, 0x6c, 0x64, 0x65, 0x72, 0x5f, 0x69, 0x64, 0x18,
	0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08, 0x66, 0x6f, 0x6c, 0x64, 0x65, 0x72, 0x49, 0x64, 0x32,
	0x68, 0x0a, 0x10, 0x52, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x69, 0x6e, 0x67, 0x53, 0x65, 0x72, 0x76,
	0x69, 0x63, 0x65, 0x12, 0x54, 0x0a, 0x0d, 0x53, 0x61, 0x76, 0x65, 0x52, 0x65, 0x63, 0x6f, 0x72,
	0x64, 0x69, 0x6e, 0x67, 0x12, 0x1f, 0x2e, 0x72, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x69, 0x6e, 0x67,
	0x2e, 0x53, 0x61, 0x76, 0x65, 0x52, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x69, 0x6e, 0x67, 0x52, 0x65,
	0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x20, 0x2e, 0x72, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x69, 0x6e,
	0x67, 0x2e, 0x53, 0x61, 0x76, 0x65, 0x52, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x69, 0x6e, 0x67, 0x52,
	0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x00, 0x42, 0x2f, 0x5a, 0x2d, 0x73, 0x79, 0x6e,
	0x78, 0x72, 0x6f, 0x6e, 0x64, 0x72, 0x69, 0x76, 0x65, 0x2f, 0x70, 0x6b, 0x67, 0x2f, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x2f, 0x72, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x69, 0x6e, 0x67, 0x5f, 0x76, 0x31,
	0x2f, 0x72, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x69, 0x6e, 0x67, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x33,
}

var (
	file_recording_proto_rawDescOnce sync.Once
	file_recording_proto_rawDescData = file_recording_proto_rawDesc
)

func file_recording_proto_rawDescGZIP() []byte {
	file_recording_proto_rawDescOnce.Do(func() {
		file_recording_proto_rawDescData = protoimpl.X.CompressGZIP(file_recording_proto_rawDescData)
	})
	return file_recording_proto_rawDescData
}

var file_recording_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_recording_proto_goTypes = []any{
	(*SaveRecordingRequest)(nil),  // 0: recording.SaveRecordingRequest
	(*SaveRecordingResponse)(nil), // 1: recording.SaveRecordingResponse
}
var file_recording_proto_depIdxs = []int32{
	0, // 0: recording.RecordingService.SaveRecording:input_type -> recording.SaveRecordingRequest
	1, // 1: recording.RecordingService.SaveRecording:output_type -> recording.SaveRecordingResponse
	1, // [1:2] is the sub-list for method output_type
	0, // [0:1] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_recording_proto_init() }
func file_recording_proto_init() {
	if File_recording_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_recording_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_recording_proto_goTypes,
		DependencyIndexes: file_recording_proto_depIdxs,
		MessageInfos:      file_recording_proto_msgTypes,
	}.Build()
	File_recording_proto = out.File
	file_recording_proto_rawDesc = nil
	file_recording_proto_goTypes = nil
	file_recording_proto_depIdxs = nil
}
