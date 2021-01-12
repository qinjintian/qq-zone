package office

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"github.com/360EntSecGroup-Skylar/excelize/v2"
	"path/filepath"
	"qq-zone/utils/filer"
	"qq-zone/utils/helper"
	"time"
	"encoding/csv"
)

/**
 * 导出数据到excel文档第一张工作簿
 * @param [][]interface{} data 内容
 * @param string msgs 可变参数，参数顺序 0：target(目标地址)、1：sheetName(工作簿名，默认Sheet1)
 */
func ExcelWrite(data [][]interface{}, msgs ...string) (map[string]string, error) {
	if len(data) < 1 {
		return nil, errors.New("Did not check the data to be written")
	}

	var (
		targetDir = "storage/tmp"
		filename = fmt.Sprintf("%s_%s.xlsx", time.Now().Format("20060102150405"), helper.GetRandomString(4))
		target = fmt.Sprintf("%s/%s", targetDir, filename)
	)
	if len(msgs) > 0 {
		target = msgs[0]
		extension := filepath.Ext(target)
		if extension != ".xlsx" && extension != ".xls" {
			return nil, errors.New("The file extension must be .xlsx or .xls")
		}
		targetDir = filepath.Dir(target)
		filename = filepath.Base(target)
		target = fmt.Sprintf("%s/%s", targetDir, filename)
	}

	if !filer.IsDir(targetDir) {
		os.MkdirAll(targetDir, os.ModePerm)
	}

	sheetName := "Sheet1"
	if len(msgs) > 1 {
		sheetName = msgs[1]
	}

	file := excelize.NewFile()
	for i, row := range data {
		file.SetSheetRow(sheetName, fmt.Sprintf("%v%v", "A", i+1), &row)
	}
	file.SetActiveSheet(file.NewSheet(sheetName))
	maxColumnString, _ := excelize.ColumnNumberToName(len(data))
	file.SetColWidth(sheetName, "A", maxColumnString, 15)
	style, _ := file.NewStyle(`{"alignment":{"horizontal":"center", "vertical":"center"}}`)
	file.SetCellStyle(sheetName, "A1", fmt.Sprintf("%s%d", maxColumnString, len(data)), style)
	if err := file.SaveAs(target); err != nil {
		return nil, err
	}

	resp := make(map[string]string)
	resp["filename"] = filename
	resp["dir"] = targetDir
	resp["path"] = target
	return resp, nil
}

/**
 * 读取excel文档内容
 * @param string dst 文件路径
 * @param interface{} msgs 可变参数，参数顺序 0：sheetName(工作簿名，默认Sheet1)、1：skip(跳过文档前几行，默认返回全部内容)
 */
func ExcelRead(dst string, msgs ...interface{}) ([][]string, error) {
	if !filer.IsFile(dst) {
		return nil, errors.New("Invalid file address")
	}

	extension := filepath.Ext(dst)
	if extension != ".xlsx" && extension != ".xls" {
		return nil, errors.New("The file extension must be .xlsx or .xls")
	}

	sheetName := "Sheet1"
	if len(msgs) > 0 {
		sheetName = msgs[0].(string)
	}

	skip := 0
	if len(msgs) > 1 {
		skip = msgs[1].(int)
	}

	file, err := excelize.OpenFile(dst)
	if err != nil {
		return nil, err
	}

	rows, err := file.GetRows(sheetName)
	if err != nil {
		return nil, err
	}

	if len(rows) < 1 {
		return nil, errors.New("The document has no content")
	}

	if skip > len(rows) {
		return nil, errors.New("The number of skipped lines exceeds the total number of lines in the document")
	}
	return rows[skip:], nil
}

/**
 * 读取csv文档内容
 * @param string target 文件路径
 * @param interface{} msgs 可变参数，参数顺序 0：skip(跳过文档前几行，默认返回全部内容)
 */
func CsvRead(target string, msgs ...interface{}) ([][]string, error) {
	file, err := os.Open(target)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	rows := make([][]string, 0)
	reader := csv.NewReader(bufio.NewReader(file))
	for {
		line, err := reader.Read()
		if err == io.EOF {
			break
		} else if err != nil {
			return rows, nil
		}

		row := make([]string, 0)
		for _, cell := range line {
			s, _ := helper.GbkToUtf8(cell)
			row = append(row, s)
		}
		rows = append(rows, row)
	}

	skip := 0
	if len(msgs) > 0 {
		skip = msgs[0].(int)
	}
	return rows[skip:], nil
}

/**
 * 写入数据到CSV文档
 * @param [][]interface{} rows 内容
 * @param string msgs 可变参数，参数顺序 0：target(目标路径)
 */
func CsvWrite(rows [][]string, msgs ...string) (map[string]string, error) {
	var (
		targetDir = "storage/tmp"
		filename = fmt.Sprintf("%s_%s.csv", time.Now().Format("20060102150405"), helper.GetRandomString(4))
		target = fmt.Sprintf("%s/%s", targetDir, filename)
	)

	if len(msgs) > 0 {
		target = msgs[0]
		if filepath.Ext(target) != ".csv" {
			return nil, errors.New("This is not a file with the suffix csv")
		}
		targetDir = filepath.Dir(target)
		filename = filepath.Base(target)
	}

	if !filer.IsDir(targetDir) {
		os.MkdirAll(targetDir, os.ModePerm)
	}

	file, err := os.Create(target)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	for key, value := range rows {
		if writer.Write(value) != nil {
			return nil, fmt.Errorf("Failed to write data in row %d, error message: %s", (key+1), err.Error())
		}
	}

	resp := make(map[string]string)
	resp["filename"] = filename
	resp["dir"] = targetDir
	resp["path"] = target
	return resp, nil
}