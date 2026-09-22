// upload_default_test.go 盯 uploadAllowed 的三方关系：默认收上传、-public
// 要显式打开、-read-only 压过前两者。
//
// 这段逻辑本身只有六行，但它决定的是"公网上这个目录能不能被人写"，
// 搞错一个分支的代价和代码量完全不成比例，所以每个组合都摆出来。
package main

import "testing"

func TestUploadAllowed(t *testing.T) {
	cases := []struct {
		name                     string
		public, upload, readOnly bool
		want                     bool
	}{
		// 默认：有鉴权，收上传。要求用户记得敲 -upload 等于把功能藏起来
		{"plain run", false, false, false, true},
		{"explicit -upload", false, true, false, true},

		// -public 把门全开了，再默认可写就是"谁都能塞东西的公网目录"，
		// 这不该是敲一行 -public 的副作用
		{"public alone stays read-only", true, false, false, false},
		{"public with explicit -upload", true, true, false, true},

		// -read-only 是明确的关掉意图，压过上面全部
		{"read-only", false, false, true, false},
		{"read-only beats -upload", false, true, true, false},
		{"read-only beats public+upload", true, true, true, false},
		{"read-only with public", true, false, true, false},
	}
	for _, c := range cases {
		if got := uploadAllowed(c.public, c.upload, c.readOnly); got != c.want {
			t.Errorf("%s: uploadAllowed(public=%v, upload=%v, readOnly=%v) = %v, want %v",
				c.name, c.public, c.upload, c.readOnly, got, c.want)
		}
	}
}
