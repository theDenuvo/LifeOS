package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

type recordingPublisher struct {
	types []string
}

func (p *recordingPublisher) Publish(eventType string, _ any) {
	p.types = append(p.types, eventType)
}

func TestServicePublishesAfterCommit(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	uow := NewFakeUnitOfWork()
	publisher := &recordingPublisher{}
	service := NewService(uow, fixedClock{now: now}, WithPublisher(publisher))

	created, err := service.CreateTask(context.Background(), CreateTaskInput{Title: "Задача"}, task.ActorUser)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := service.UpdateTask(context.Background(), created.Task.ID, created.Version,
		UpdateTaskInput{Title: Some("Новая")}, task.ActorUser); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := service.CompleteTask(context.Background(), created.Task.ID, 2, task.ActorUser); err != nil {
		t.Fatalf("complete: %v", err)
	}

	want := []string{"task.created", "task.updated", "task.completed"}
	if len(publisher.types) != len(want) {
		t.Fatalf("published %v, want %v", publisher.types, want)
	}
	for i := range want {
		if publisher.types[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, publisher.types[i], want[i])
		}
	}
}

func TestServiceDoesNotPublishRolledBackWork(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	uow := NewFakeUnitOfWork()
	publisher := &recordingPublisher{}
	service := NewService(uow, fixedClock{now: now}, WithPublisher(publisher))

	created, err := service.CreateTask(context.Background(), CreateTaskInput{Title: "Задача"}, task.ActorUser)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	publisher.types = nil

	// Stale version: the transaction fails, so nothing may be published.
	if _, err := service.UpdateTask(context.Background(), created.Task.ID, created.Version+5,
		UpdateTaskInput{Title: Some("Устаревшая")}, task.ActorUser); err == nil {
		t.Fatal("stale update unexpectedly succeeded")
	}
	if len(publisher.types) != 0 {
		t.Errorf("published %v after a failed transaction", publisher.types)
	}
}
