package storage

import "inst/internal/models"

type Storage interface {
	SaveComment(comment *models.Comment) error
	Close() error
}
