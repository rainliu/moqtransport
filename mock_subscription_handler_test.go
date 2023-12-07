// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/mengelbart/moqtransport (interfaces: SubscriptionHandler)
//
// Generated by this command:
//
//	mockgen -build_flags=-tags=gomock -package moqtransport -self_package github.com/mengelbart/moqtransport -destination mock_subscription_handler_test.go github.com/mengelbart/moqtransport SubscriptionHandler
//
// Package moqtransport is a generated GoMock package.
package moqtransport

import (
	reflect "reflect"
	time "time"

	gomock "go.uber.org/mock/gomock"
)

// MockSubscriptionHandler is a mock of SubscriptionHandler interface.
type MockSubscriptionHandler struct {
	ctrl     *gomock.Controller
	recorder *MockSubscriptionHandlerMockRecorder
}

// MockSubscriptionHandlerMockRecorder is the mock recorder for MockSubscriptionHandler.
type MockSubscriptionHandlerMockRecorder struct {
	mock *MockSubscriptionHandler
}

// NewMockSubscriptionHandler creates a new mock instance.
func NewMockSubscriptionHandler(ctrl *gomock.Controller) *MockSubscriptionHandler {
	mock := &MockSubscriptionHandler{ctrl: ctrl}
	mock.recorder = &MockSubscriptionHandlerMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockSubscriptionHandler) EXPECT() *MockSubscriptionHandlerMockRecorder {
	return m.recorder
}

// handle mocks base method.
func (m *MockSubscriptionHandler) handle(arg0, arg1 string, arg2 *SendTrack) (uint64, time.Duration, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "handle", arg0, arg1, arg2)
	ret0, _ := ret[0].(uint64)
	ret1, _ := ret[1].(time.Duration)
	ret2, _ := ret[2].(error)
	return ret0, ret1, ret2
}

// handle indicates an expected call of handle.
func (mr *MockSubscriptionHandlerMockRecorder) handle(arg0, arg1, arg2 any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "handle", reflect.TypeOf((*MockSubscriptionHandler)(nil).handle), arg0, arg1, arg2)
}