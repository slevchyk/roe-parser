package models

import ics "github.com/arran4/golang-ical"

type WorkGroup struct {
    ID          string
    ColumnIndex int
    Calendar    *ics.Calendar
    EventCount  int
}