package bundle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
)

type publicationNamespaceRule struct {
	name     string
	classify func(string) (kind string, reserved bool)
}

type publicationNamespaceDiscovery struct {
	namespace    string
	kind         string
	directory    string
	name         string
	protocolName string
	relative     string
	parentInfo   os.FileInfo
	info         os.FileInfo
}

// discoverPublicationNamespaces is the only transaction namespace walker. It
// inventories every configured protocol in one lexical traversal, skips only
// the root-relative Store metadata directory, and performs no mutation.
func discoverPublicationNamespaces(
	ctx context.Context,
	root *os.Root,
	rules []publicationNamespaceRule,
) ([]publicationNamespaceDiscovery, error) {
	pending := []string{""}
	seen := make(map[string]struct{})
	var discoveries []publicationNamespaceDiscovery
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sort.Strings(pending)
		directory := pending[0]
		pending = pending[1:]
		if _, exists := seen[directory]; exists {
			continue
		}
		seen[directory] = struct{}{}

		parent, err := openDirectory(root, directory)
		if err != nil {
			return nil, fmt.Errorf(
				"inspect publication namespace directory %q: %w",
				directory,
				err,
			)
		}
		parentInfo, err := parent.Lstat(".")
		if err != nil || !parentInfo.IsDir() {
			_ = parent.Close()
			return nil, errors.Join(err, ErrNotDirectory)
		}
		file, err := parent.Open(".")
		if err != nil {
			_ = parent.Close()
			return nil, err
		}
		entries, readErr := file.ReadDir(-1)
		closeFileErr := file.Close()
		if readErr != nil || closeFileErr != nil {
			_ = parent.Close()
			return nil, errors.Join(readErr, closeFileErr)
		}
		sort.Slice(entries, func(left, right int) bool {
			return entries[left].Name() < entries[right].Name()
		})
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				_ = parent.Close()
				return nil, err
			}
			name := entry.Name()
			relative := pathJoin(directory, name)
			matched := false
			for _, rule := range rules {
				kind, reserved := rule.classify(name)
				if !reserved {
					continue
				}
				if matched {
					_ = parent.Close()
					return nil, publicationConflict(
						"artifact matches multiple publication protocols",
						nil,
					)
				}
				info, statErr := parent.Lstat(name)
				if statErr != nil {
					_ = parent.Close()
					return nil, statErr
				}
				discoveries = append(discoveries, publicationNamespaceDiscovery{
					namespace:    rule.name,
					kind:         kind,
					directory:    directory,
					name:         name,
					protocolName: name,
					relative:     relative,
					parentInfo:   parentInfo,
					info:         info,
				})
				matched = true
			}
			if matched {
				continue
			}
			info, err := parent.Lstat(name)
			if err != nil {
				_ = parent.Close()
				return nil, err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				continue
			}
			if directory == "" && name == ".okf" {
				continue
			}
			pending = append(pending, relative)
		}
		if err := parent.Close(); err != nil {
			return nil, err
		}
	}
	sort.Slice(discoveries, func(left, right int) bool {
		return discoveries[left].relative < discoveries[right].relative
	})
	return discoveries, nil
}

func revalidatePublicationNamespaceSnapshot(
	ctx context.Context,
	root *os.Root,
	rules []publicationNamespaceRule,
	expected []publicationNamespaceDiscovery,
) error {
	current, err := discoverPublicationNamespaces(ctx, root, rules)
	if err != nil {
		return err
	}
	if len(current) != len(expected) {
		return publicationConflict("publication inventory changed", nil)
	}
	for index := range expected {
		if err := ctx.Err(); err != nil {
			return err
		}
		left, right := expected[index], current[index]
		if left.namespace != right.namespace ||
			left.kind != right.kind ||
			left.directory != right.directory ||
			left.name != right.name ||
			left.relative != right.relative ||
			!os.SameFile(left.parentInfo, right.parentInfo) ||
			!samePublicationEntryObservation(left.info, right.info) {
			return publicationConflict("publication inventory observation changed", nil)
		}
	}
	return nil
}

func samePublicationEntryObservation(left, right os.FileInfo) bool {
	if left == nil || right == nil {
		return left == right
	}
	return os.SameFile(left, right) &&
		left.Mode() == right.Mode() &&
		left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime())
}

func verifyPublicationParent(
	root *os.Root,
	directory string,
	expected os.FileInfo,
) error {
	parent, err := openDirectory(root, directory)
	if err != nil {
		return publicationConflict("publication parent detached", err)
	}
	defer parent.Close()
	info, err := parent.Lstat(".")
	if err != nil || !info.IsDir() || expected == nil || !os.SameFile(expected, info) {
		return publicationConflict("publication parent identity changed", err)
	}
	return nil
}

func publicationPathPhysicalKey(root *os.Root, relative string) (string, error) {
	directory, leaf := path.Split(relative)
	directory = path.Clean(directory)
	if directory == "." {
		directory = ""
	}
	parent, err := openDirectory(root, directory)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	info, err := parent.Lstat(".")
	if err != nil {
		return "", err
	}
	identity, err := physicalPublicationRootIdentity(parent)
	if err != nil && !errors.Is(err, ErrPublicationCapabilityUnsupported) {
		return "", err
	}
	if identity == "" {
		identity = fmt.Sprintf("%v:%d:%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
	}
	return identity + "\x00" + asciiFoldString(leaf), nil
}

func asciiFoldString(value string) string {
	out := []byte(value)
	for index, character := range out {
		if character >= 'A' && character <= 'Z' {
			out[index] = character + ('a' - 'A')
		}
	}
	return string(out)
}

func publicationEntryAbsent(parent *os.Root, name string) bool {
	_, err := parent.Lstat(name)
	return errors.Is(err, fs.ErrNotExist)
}
