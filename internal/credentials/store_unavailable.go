//go:build !windows && (!darwin || !cgo)

package credentials

type Store struct{}

func New() *Store { return &Store{} }

func (*Store) Has(string) (bool, error)   { return false, nil }
func (*Store) Get(string) (string, error) { return "", ErrUnavailable }
func (*Store) Set(string, string) error   { return ErrUnavailable }
func (*Store) Delete(string) error        { return nil }
