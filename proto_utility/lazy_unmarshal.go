package libatframe_utils_proto_utility

import "google.golang.org/protobuf/proto"

type LazyUnmarshalProtobufMessage struct {
	rawData      []byte
	unmarshal    bool
	unmarshalErr error
	message      proto.Message
}

func CreateLazyUnmarshalProtobufMessage(rawData []byte) *LazyUnmarshalProtobufMessage {
	return &LazyUnmarshalProtobufMessage{
		rawData:      rawData,
		unmarshal:    false,
		unmarshalErr: nil,
		message:      nil,
	}
}

func (l *LazyUnmarshalProtobufMessage) GetMessage() (proto.Message, error) {
	if !l.unmarshal {
		l.unmarshalErr = proto.Unmarshal(l.rawData, l.message)
		l.unmarshal = true
	}
	return l.message, l.unmarshalErr
}
