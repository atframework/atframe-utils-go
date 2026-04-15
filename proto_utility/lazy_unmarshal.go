package libatframe_utils_proto_utility

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type LazyUnmarshalProtobufMessage struct {
	rawData      []byte
	unmarshal    bool
	unmarshalErr error
	messageType  protoreflect.MessageType
	message      proto.Message
	onUnmarshal  func(proto.Message, error)
}

func CreateLazyUnmarshalProtobufMessage(rawData []byte, messageType protoreflect.MessageType, onUnmarshal func(proto.Message, error)) *LazyUnmarshalProtobufMessage {
	return &LazyUnmarshalProtobufMessage{
		rawData:      rawData,
		unmarshal:    false,
		unmarshalErr: nil,
		messageType:  messageType,
		message:      nil,
		onUnmarshal:  onUnmarshal,
	}
}

func (l *LazyUnmarshalProtobufMessage) GetMessage() (proto.Message, error) {
	if !l.unmarshal {
		l.message = l.messageType.New().Interface()
		l.unmarshalErr = proto.Unmarshal(l.rawData, l.message)
		l.unmarshal = true
		if l.onUnmarshal != nil {
			l.onUnmarshal(l.message, l.unmarshalErr)
		}
	}
	return l.message, l.unmarshalErr
}

type LazyUnmarshalProtobufMessageSpecific[Type proto.Message] struct {
	inner *LazyUnmarshalProtobufMessage
}

func CreateLazyUnmarshalProtobufMessageSpecific[Type proto.Message](l *LazyUnmarshalProtobufMessage) *LazyUnmarshalProtobufMessageSpecific[Type] {
	return &LazyUnmarshalProtobufMessageSpecific[Type]{
		inner: l,
	}
}

func (t *LazyUnmarshalProtobufMessageSpecific[Type]) GetMessage() (Type, error) {
	msg, err := t.inner.GetMessage()
	if err != nil {
		var zero Type
		return zero, err
	}
	return msg.(Type), nil
}
