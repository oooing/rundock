package main

import (
	"debug/pe"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Structural validation is deliberately local: do not launch either payload.
// Download authenticity remains the updater's SHA-256 verification responsibility.
func validatePE(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() < 512 || info.Size() > 512*1024*1024 {
		return errors.New("invalid executable file or size")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var signature [2]byte
	if _, err := f.ReadAt(signature[:], 0); err != nil || string(signature[:]) != "MZ" {
		return errors.New("missing executable DOS header")
	}
	image, err := pe.NewFile(f)
	if err != nil {
		return err
	}
	defer image.Close()
	header, ok := image.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || header.Magic != 0x20b || image.Machine != pe.IMAGE_FILE_MACHINE_AMD64 || image.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || image.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		return errors.New("expected a Windows x64 executable")
	}
	if len(image.Sections) == 0 || len(image.Sections) > 96 || header.SizeOfHeaders == 0 || int64(header.SizeOfHeaders) > info.Size() || header.SizeOfImage == 0 {
		return errors.New("invalid executable layout")
	}
	entry := false
	for _, section := range image.Sections {
		if section.Size > 0 && (section.Offset < header.SizeOfHeaders || uint64(section.Offset)+uint64(section.Size) > uint64(info.Size())) {
			return errors.New("truncated executable section")
		}
		end := uint64(section.VirtualAddress) + uint64(max(section.VirtualSize, section.Size))
		if end > uint64(header.SizeOfImage) {
			return errors.New("section exceeds executable image")
		}
		if section.Characteristics&pe.IMAGE_SCN_MEM_EXECUTE != 0 && header.AddressOfEntryPoint >= section.VirtualAddress && uint64(header.AddressOfEntryPoint) < end {
			entry = true
		}
	}
	if !entry {
		return errors.New("missing executable entry point")
	}
	return nil
}

func validatePayload(root string) error {
	for _, name := range names {
		if err := validatePE(filepath.Join(root, name)); err != nil {
			return fmt.Errorf("cannot commit %s: %w", name, err)
		}
	}
	return nil
}
