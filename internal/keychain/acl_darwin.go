//go:build darwin

package keychain

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>
#include <string.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

// The SecKeychain* / SecACL* family is deprecated without a replacement
// (the data-protection keychain has no per-item ACL editing). Accepted for
// this test-time-only path.
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"

// Rewrites every decrypt-authorizing ACL entry of access to a NULL
// application list, which Security.framework defines as "any application
// may access without confirmation". Only legal before the access is bound
// to an item; editing the ACL of an existing item requires interactive
// user confirmation (errSecUserCanceled / -128 otherwise).
static OSStatus cft_relax_access(SecAccessRef access) {
	CFArrayRef acls = NULL;
	OSStatus status = SecAccessCopyACLList(access, &acls);
	if (status != errSecSuccess) {
		return status;
	}
	for (CFIndex i = 0; i < CFArrayGetCount(acls) && status == errSecSuccess; i++) {
		SecACLRef acl = (SecACLRef)CFArrayGetValueAtIndex(acls, i);
		CFArrayRef auths = SecACLCopyAuthorizations(acl);
		Boolean decrypts = auths != NULL && CFArrayContainsValue(
			auths, CFRangeMake(0, CFArrayGetCount(auths)), kSecACLAuthorizationDecrypt);
		if (auths) CFRelease(auths);
		if (!decrypts) {
			continue;
		}
		CFArrayRef apps = NULL;
		CFStringRef desc = NULL;
		SecKeychainPromptSelector prompt;
		status = SecACLCopyContents(acl, &apps, &desc, &prompt);
		if (status != errSecSuccess) {
			break;
		}
		status = SecACLSetContents(acl, NULL, desc, prompt);
		if (apps) CFRelease(apps);
		if (desc) CFRelease(desc);
	}
	CFRelease(acls);
	return status;
}

// Creates the (service, account) generic password item with an any-app
// access. The item must not already exist. Empty path → login keychain.
static OSStatus cft_add_any_app_item(const char *path, const char *service, const char *account,
		const void *data, UInt32 dataLen) {
	SecKeychainRef keychain = NULL;
	OSStatus status = errSecSuccess;
	if (path != NULL && path[0] != '\0') {
		status = SecKeychainOpen(path, &keychain);
		if (status != errSecSuccess) {
			return status;
		}
	}

	CFStringRef label = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
	SecAccessRef access = NULL;
	status = SecAccessCreate(label, NULL, &access);
	if (label) CFRelease(label);
	if (status == errSecSuccess) {
		status = cft_relax_access(access);
	}
	if (status == errSecSuccess) {
		SecKeychainAttribute attrs[] = {
			{ kSecServiceItemAttr, (UInt32)strlen(service), (void *)service },
			{ kSecAccountItemAttr, (UInt32)strlen(account), (void *)account },
		};
		SecKeychainAttributeList attrList = { 2, attrs };
		status = SecKeychainItemCreateFromContent(kSecGenericPasswordItemClass,
			&attrList, dataLen, data, keychain, access, NULL);
	}
	if (access) CFRelease(access);
	if (keychain) CFRelease(keychain);
	return status;
}

#pragma clang diagnostic pop
*/
import "C"

import (
	"fmt"
	"unsafe"

	kc "github.com/99designs/go-keychain"
)

// setAnyApp replaces (service, account) with an entry any process can read
// without a confirmation prompt. The ACL must be shaped before the item is
// created, so this is a delete + create rather than an in-place update.
func (d Darwin) setAnyApp(service, account string, data []byte) error {
	if err := d.Delete(service, account); err != nil {
		return err
	}

	cPath := C.CString(d.path)
	cService := C.CString(service)
	cAccount := C.CString(account)
	defer C.free(unsafe.Pointer(cPath))
	defer C.free(unsafe.Pointer(cService))
	defer C.free(unsafe.Pointer(cAccount))
	var p unsafe.Pointer
	if len(data) > 0 {
		p = unsafe.Pointer(&data[0])
	}

	status := C.cft_add_any_app_item(cPath, cService, cAccount, p, C.UInt32(len(data)))
	if status != C.errSecSuccess {
		return fmt.Errorf("keychain: set with any-app ACL: %w", kc.Error(status))
	}
	return nil
}
