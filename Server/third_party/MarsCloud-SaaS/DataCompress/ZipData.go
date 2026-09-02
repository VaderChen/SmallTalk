package DataCompress

// -------------------------------------------------------------------------------------
import (
	"bytes"
	"io"
	"os"

	// 使用支援加密的 zip 庫，避免手動實作 ZipDecryptInputStream
	"github.com/alexmullins/zip"
)

// -------------------------------------------------------------------------------------
//
// -------------------------------------------------------------------------------------
func UnZip(_fn string, _pwd string) []byte {
	if _fn == "" {
		return nil
	}

	// 開啟檔案
	_f, _err := os.Open(_fn)
	if _err != nil {
		return nil
	}
	defer _f.Close()

	// 取得檔案資訊以利讀取
	_info, _err := _f.Stat()
	if _err != nil {
		return nil
	}

	return unZipCore(_f, _info.Size(), _pwd)
}

// -------------------------------------------------------------------------------------
// UnZipBytes 從位元組陣列解壓縮 (支援密碼)
func UnZipBytes(_src []byte, _pwd string) []byte {
	if _src == nil {
		return nil
	}

	if len(_src) < 60 {
		return nil
	}

	_reader := bytes.NewReader(_src)
	return unZipCore(_reader, int64(len(_src)), _pwd)
}

// -------------------------------------------------------------------------------------
// unZipCore 解壓縮核心邏輯
func unZipCore(_r io.ReaderAt, _size int64, _pwd string) []byte {
	_zipReader, _err := zip.NewReader(_r, _size)
	if _err != nil {
		return nil
	}

	// 遍歷壓縮檔內的檔案 (對應 Java getNextEntry)
	for _, _file := range _zipReader.File {
		// 如果有密碼，設定解密密碼
		if _pwd != "" {
			_file.SetPassword(_pwd)
		}

		_rc, _err := _file.Open()
		if _err != nil {
			continue
		}

		_buf := new(bytes.Buffer)
		_, _err = io.Copy(_buf, _rc)
		_rc.Close()

		if _err == nil {
			// 回傳第一個檔案的內容 (維持 Java 原始邏輯)
			return _buf.Bytes()
		}
	}

	return nil
}

// -------------------------------------------------------------------------------------
// Zip 將位元組陣列進行壓縮 (支援密碼與壓縮等級)
func Zip(_data []byte, _pwd string, _compressLevel int) []byte {
	if _data == nil {
		return nil
	}

	_buf := new(bytes.Buffer)
	_zipWriter := zip.NewWriter(_buf)

	// 建立 Entry 名稱為 "data" (對應 Java setFileNameInZip("data"))
	_w, _err := _zipWriter.Encrypt("data", _pwd)
	if _err != nil {
		return nil
	}

	_, _err = _w.Write(_data)
	if _err != nil {
		return nil
	}

	_err = _zipWriter.Close()
	if _err != nil {
		return nil
	}

	return _buf.Bytes()
}

// -------------------------------------------------------------------------------------
// ZipDefault 使用預設等級 (7) 進行壓縮
func ZipDefault(_data []byte, _pwd string) []byte {
	// 注意：Go 的 zip 庫壓縮等級通常由 Writer 決定，此處維持與 Java 相同的傳參結構
	return Zip(_data, _pwd, 7)
}

// -------------------------------------------------------------------------------------
