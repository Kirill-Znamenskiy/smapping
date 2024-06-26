/*
mapping
Golang mapping structure
*/

package smapping

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	s "strings"
	"time"
)

// Mapped simply an alias
type Mapped map[string]interface{}

type MapEncoder interface {
	MapEncode() (interface{}, error)
}

var mapEncoderI = reflect.TypeOf((*MapEncoder)(nil)).Elem()

type MapDecoder interface {
	MapDecode(interface{}) error
}

var mapDecoderI = reflect.TypeOf((*MapDecoder)(nil)).Elem()

func extractValue(x interface{}) reflect.Value {
	var result reflect.Value
	switch v := x.(type) {
	case reflect.Value:
		result = v
	default:
		result = reflect.ValueOf(x)
		for result.Type().Kind() == reflect.Ptr {
			result = result.Elem()
		}
		if result.Type().Kind() != reflect.Struct {
			typ := reflect.StructOf([]reflect.StructField{})
			result = reflect.Zero(typ)
		}
	}
	return result
}

/*
MapFields maps between struct to mapped interfaces{}.
The argument must be (zero or minterface{} pointers to) struct or else it will be ignored.
Now it's implemented as MapTags with empty tag "".

Only map the exported fields.
*/
func MapFields(x interface{}) Mapped {
	return MapTags(x, "")
}

func tagHead(tag string) string {
	return s.Split(tag, ",")[0]
}

func isValueNil(v reflect.Value) bool {
	for _, kind := range []reflect.Kind{
		reflect.Ptr, reflect.Slice, reflect.Map,
		reflect.Chan, reflect.Interface, reflect.Func,
	} {
		if v.Kind() == kind && v.IsNil() {
			return true
		}

	}
	return false
}

func getValTag(fieldval reflect.Value, tag string) interface{} {
	var resval interface{}
	if isValueNil(fieldval) {
		return nil
	}
	if fieldval.Type().Name() == "Time" ||
		reflect.Indirect(fieldval).Type().Name() == "Time" {
		resval = fieldval.Interface()
	} else if typof := fieldval.Type(); typof.Implements(mapEncoderI) ||
		reflect.PtrTo(typof).Implements(mapEncoderI) {
		valx, ok := fieldval.Interface().(MapEncoder)
		if !ok {
			return nil
		}
		val, err := valx.MapEncode()
		if err != nil {
			val = nil
		}
		resval = val
	} else {
		switch fieldval.Kind() {
		case reflect.Struct:
			resval = MapTags(fieldval, tag)
		case reflect.Ptr:
			indirect := reflect.Indirect(fieldval)
			if indirect.Kind() < reflect.Array || indirect.Kind() == reflect.String {
				resval = indirect.Interface()
			} else {
				resval = MapTags(fieldval.Elem(), tag)
			}
		case reflect.Slice:
			placeholder := make([]interface{}, fieldval.Len())
			for i := 0; i < fieldval.Len(); i++ {
				fieldvalidx := fieldval.Index(i)
				theval := getValTag(fieldvalidx, tag)
				placeholder[i] = theval
			}
			resval = placeholder
		default:
			resval = fieldval.Interface()
		}

	}
	return resval
}

/*
MapTags maps the tag value of defined field tag name. This enable
various field extraction that will be mapped to mapped interfaces{}.
*/
func MapTags(x interface{}, tag string) Mapped {
	result := make(Mapped)
	value := extractValue(x)
	if !value.IsValid() {
		return nil
	}
	xtype := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := xtype.Field(i)
		if field.PkgPath != "" {
			continue
		}
		fieldval := value.Field(i)
		if tag == "" {
			result[field.Name] = getValTag(fieldval, tag)
		} else if tagvalue, ok := field.Tag.Lookup(tag); ok {
			result[tagHead(tagvalue)] = getValTag(fieldval, tag)
		}
	}
	return result
}

/*
MapTagsWithDefault maps the tag with optional fallback tags. This to enable
tag differences when there are only few difference with the default “json“
tag.
*/
func MapTagsWithDefault(x interface{}, tag string, defs ...string) Mapped {
	result := make(Mapped)
	value := extractValue(x)
	if !value.IsValid() {
		return nil
	}
	xtype := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := xtype.Field(i)
		if field.PkgPath != "" {
			continue
		}
		var (
			tagval string
			ok     bool
		)
		if tagval, ok = field.Tag.Lookup(tag); ok {
			result[tagHead(tagval)] = getValTag(value.Field(i), tag)
		} else {
			for _, deftag := range defs {
				if tagval, ok = field.Tag.Lookup(deftag); ok {
					result[tagHead(tagval)] = getValTag(value.Field(i), deftag)
					break // break from looping the defs
				}
			}
		}
	}
	return result
}

// MapTagsFlatten is to flatten mapped object with specific tag. The limitation
// of this flattening that it can't have duplicate tag name and it will give
// incorrect result because the older value will be written with newer map field value.
func MapTagsFlatten(x interface{}, tag string) Mapped {
	result := make(Mapped)
	value := extractValue(x)
	if !value.IsValid() {
		return nil
	}
	xtype := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := xtype.Field(i)
		if field.PkgPath != "" {
			continue
		}
		fieldval := value.Field(i)
		isStruct := reflect.Indirect(fieldval).Type().Kind() == reflect.Struct
		if tagvalue, ok := field.Tag.Lookup(tag); ok && !isStruct {
			key := tagHead(tagvalue)
			result[key] = fieldval.Interface()
			continue
		}
		fieldval = reflect.Indirect(fieldval)
		if !isStruct {
			continue
		}
		nests := MapTagsFlatten(fieldval, tag)
		for k, v := range nests {
			result[k] = v
		}
	}
	return result
}

func isTime(typ reflect.Type) bool {
	return typ.Name() == "Time" || typ.String() == "*time.Time"
}
func handleTime(layout, format string, typ reflect.Type) (reflect.Value, error) {
	t, err := time.Parse(layout, format)
	var resval reflect.Value
	if err != nil {
		return resval, fmt.Errorf("time conversion: %s", err.Error())
	}
	if typ.Kind() == reflect.Ptr {
		resval = reflect.New(typ).Elem()
		resval.Set(reflect.ValueOf(&t))
	} else {
		resval = reflect.ValueOf(&t).Elem()

	}
	return resval, err
}

func isSlicedObj(val, res reflect.Value) bool {
	return val.Type().Kind() == reflect.Slice &&
		res.Kind() == reflect.Slice
}

func fillMapIter(vfield, res reflect.Value, val *reflect.Value, tagname string) error {
	iter := val.MapRange()
	m := Mapped{}
	for iter.Next() {
		m[iter.Key().String()] = iter.Value().Interface()
	}
	if vfield.Kind() == reflect.Ptr {
		vval := vfield.Type().Elem()
		ptrres := reflect.New(vval).Elem()
		mapf := make(map[string]reflect.StructField)
		if tagname != "" {
			populateMapFieldsTag(mapf, tagname, ptrres)
		}
		for k, v := range m {
			_, err := setFieldFromTag(ptrres, tagname, k, v, mapf)
			if err != nil {
				return fmt.Errorf("ptr nested error: %s", err.Error())
			}
		}
		*val = ptrres.Addr()
	} else {
		if err := FillStructByTags(res, m, tagname); err != nil {
			return fmt.Errorf("nested error: %s", err.Error())
		}
		*val = res
	}
	return nil
}

func fillTime(vfield reflect.Value, val *reflect.Value) error {
	if (*val).Type().Name() == "string" {
		newval, err := handleTime(time.RFC3339, val.String(), vfield.Type())
		if err != nil {
			return fmt.Errorf("smapping Time conversion: %s", err.Error())
		}
		*val = newval
	} else if val.Type().Name() == "Time" {
		*val = reflect.Indirect(*val)
	}
	return nil
}

func scalarType(val reflect.Value) bool {
	if val.Kind() != reflect.Interface {
		return false
	}
	switch val.Interface().(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, string, []byte:
		return true

	}
	return false
}

func ptrExtract(vval, rval reflect.Value) (reflect.Value, bool) {
	acttype := rval.Type().Elem()
	newrval := reflect.New(acttype).Elem()
	gotval := false
	if newrval.Kind() < reflect.Array {
		gotval = true
		ival := vval.Interface()
		if newrval.Kind() > reflect.Bool && newrval.Kind() < reflect.Uint {
			nval := reflect.ValueOf(ival).Int()
			newrval.SetInt(nval)
		} else if newrval.Kind() > reflect.Uintptr &&
			newrval.Kind() < reflect.Complex64 {
			fval := reflect.ValueOf(ival).Float()
			newrval.SetFloat(fval)
		} else {
			newrval.Set(reflect.ValueOf(ival))
		}
	}
	return newrval, gotval
}

func fillSlice(res reflect.Value, val *reflect.Value, tagname string) error {
	for i := 0; i < val.Len(); i++ {
		vval := val.Index(i)
		rval := reflect.New(res.Type().Elem()).Elem()
		if vval.Kind() < reflect.Array {
			rval.Set(vval)
			res = reflect.Append(res, rval)
			continue
		} else if scalarType(vval) {
			if rval.Kind() == reflect.Ptr {
				if newrval, ok := ptrExtract(vval, rval); ok {
					res = reflect.Append(res, newrval.Addr())
					continue
				}
			}
			rval.Set(reflect.ValueOf(vval.Interface()))
			res = reflect.Append(res, rval)
			continue
		} else if vval.IsNil() {
			res = reflect.Append(res, reflect.Zero(rval.Type()))
			continue
		}
		newrval := rval
		if rval.Kind() == reflect.Ptr {
			var ok bool
			if newrval, ok = ptrExtract(vval, rval); ok {
				res = reflect.Append(res, newrval.Addr())
				continue
			}
		}
		m, ok := vval.Interface().(Mapped)
		if !ok && newrval.Kind() >= reflect.Array {
			m = MapTags(vval.Interface(), tagname)
		}
		err := FillStructByTags(newrval, m, tagname)
		if err != nil {
			return fmt.Errorf("cannot set an element slice")
		}
		if rval.Kind() == reflect.Ptr {
			res = reflect.Append(res, newrval.Addr())
		} else {
			res = reflect.Append(res, newrval)
		}
	}
	*val = res
	return nil
}

func populateMapFieldsTag(mapfield map[string]reflect.StructField, tagname string, obj interface{}) {
	sval := extractValue(obj)
	stype := sval.Type()
	for i := 0; i < sval.NumField(); i++ {
		field := stype.Field(i)
		if field.PkgPath != "" {
			continue
		}
		if tag, ok := field.Tag.Lookup(tagname); ok {
			mapfield[tagHead(tag)] = field
		}
	}
}

func setFieldFromTag(obj interface{}, tagname, tagvalue string,
	value interface{}, mapfield map[string]reflect.StructField) (bool, error) {
	return SetFieldFromTag(obj, tagname, tagvalue, value, mapfield)
}

func SetFieldFromTag(
	obj interface{},
	tagName, tagValue string,
	value interface{},
	tagName2structField map[string]reflect.StructField,
) (bool, error) {
	rObjVal := extractValue(obj)
	rObjType := rObjVal.Type()

	fieldName := tagValue
	if tagName == "" {
		if _, ok := rObjType.FieldByName(tagValue); !ok {
			return false, nil
		}
	} else {
		rFieldStructField, ok := tagName2structField[tagValue]
		if !ok {
			return false, nil
		}
		fieldName = rFieldStructField.Name
	}
	rField := rObjVal.FieldByName(fieldName)
	rFieldKind := rField.Kind()
	rFieldType := rField.Type()

	rValue := reflect.ValueOf(value)
	if !rValue.IsValid() {
		return false, nil
	}
	rValueKind := rValue.Kind()
	rValueType := rValue.Type()

	lcFieldZeroValue := reflect.New(rFieldType).Elem()
	if rFieldType == rValueType {
		// nothing
	} else if rValue.CanConvert(rFieldType) {
		rValue = rValue.Convert(rFieldType)
	} else if rFieldType.Implements(mapDecoderI) || reflect.PointerTo(rFieldType).Implements(mapDecoderI) {
		isPtr := rFieldType.Kind() == reflect.Ptr
		var mapval reflect.Value
		if isPtr {
			mapval = reflect.New(rFieldType.Elem())
		} else {
			mapval = reflect.New(rFieldType)
		}
		mapdecoder, ok := mapval.Interface().(MapDecoder)
		if !ok {
			return false, nil
		}
		if err := mapdecoder.MapDecode(value); err != nil {
			return false, err
		}
		if isPtr {
			rValue = reflect.ValueOf(mapdecoder)
		} else {
			rValue = reflect.Indirect(reflect.ValueOf(mapdecoder))
		}
	} else if isTime(rField.Type()) {
		if err := fillTime(rField, &rValue); err != nil {
			return false, err
		}
	} else if lcFieldZeroValue.IsValid() && rValue.Type().Name() == "Mapped" {
		if err := fillMapIter(rField, lcFieldZeroValue, &rValue, tagName); err != nil {
			return false, err
		}
	} else if isSlicedObj(rValue, lcFieldZeroValue) {
		if err := fillSlice(lcFieldZeroValue, &rValue, tagName); err != nil {
			return false, err
		}
	} else if rFieldKind == reflect.Ptr && rValueKind != reflect.Ptr && rFieldType.Elem() == rValueType {
		rNewValue := reflect.New(rValueType).Elem()
		rNewValue.Set(rValue)
		rValue = rNewValue.Addr()
	} else if rFieldKind == reflect.Ptr && rValueKind != reflect.Ptr && rValue.CanConvert(rFieldType.Elem()) {
		rNewValue := reflect.New(rValueType).Elem()
		rNewValue.Set(rValue.Convert(rFieldType.Elem()))
		rValue = rNewValue.Addr()
	} else if rFieldKind != reflect.Ptr && rValueKind == reflect.Ptr && rFieldType == rValueType.Elem() {
		rValue = rValue.Elem()
	} else if rFieldKind != reflect.Ptr && rValueKind == reflect.Ptr && rValue.Elem().CanConvert(rFieldType) {
		rValue = rValue.Elem().Convert(rFieldType)
	} else if rFieldType != rValueType {
		return false, fmt.Errorf("provided value (%#v) type %T not match field tag '%s' of tagname '%s'  of type '%v' from object",
			value, value, tagName, tagValue, rFieldType)
	}
	rField.Set(rValue)
	return true, nil
}

/*
FillStruct acts just like “json.Unmarshal“ but works with “Mapped“
instead of bytes of char that made from “json“.
*/
func FillStruct(obj interface{}, mapped Mapped) error {
	errmsg := ""
	mapf := make(map[string]reflect.StructField)
	for k, v := range mapped {
		if v == nil {
			continue
		}
		_, err := setFieldFromTag(obj, "", k, v, mapf)
		if err != nil {
			if errmsg != "" {
				errmsg += ","
			}
			errmsg += err.Error()
		}
	}
	if errmsg != "" {
		return fmt.Errorf(errmsg)
	}
	return nil
}

/*
FillStructByTags fills the field that has tagname and tagvalue
instead of Mapped key name.
*/
func FillStructByTags(obj interface{}, mapped Mapped, tagname string) error {
	errmsg := ""
	mapf := make(map[string]reflect.StructField)
	populateMapFieldsTag(mapf, tagname, obj)
	for k, v := range mapped {
		if v == nil {
			continue
		}
		_, err := setFieldFromTag(obj, tagname, k, v, mapf)
		if err != nil {
			if errmsg != "" {
				errmsg += ","
			}
			errmsg += err.Error()
		}
	}
	if errmsg != "" {
		return fmt.Errorf(errmsg)
	}
	return nil
}

// FillStructDeflate fills the nested object from flat map.
// This works by filling outer struct first and then checking its subsequent object fields.
func FillStructDeflate(obj interface{}, mapped Mapped, tagname string) error {
	errmsg := ""
	err := FillStructByTags(obj, mapped, tagname)
	if err != nil {
		errmsg = err.Error()
	}
	sval := extractValue(obj)
	for i := 0; i < sval.NumField(); i++ {
		field := sval.Field(i)
		kind := field.Kind()
		if kind == reflect.Struct {
			res := reflect.New(field.Type()).Elem()
			if err = FillStructDeflate(res, mapped, tagname); err != nil {
				if errmsg != "" {
					errmsg += ", "
				}
				errmsg += err.Error()
				continue
			}
			field.Set(res)
		} else if kind == reflect.Ptr {
			indirectField := field.Type().Elem()
			if indirectField.Kind() != reflect.Struct {
				continue
			}
			res := reflect.New(indirectField).Elem()
			if err = FillStructDeflate(res, mapped, tagname); err != nil {
				if errmsg != "" {
					errmsg += ", "
				}
				errmsg += err.Error()
				continue
			}
			field.Set(res.Addr())
		}
	}
	if errmsg != "" {
		return fmt.Errorf(errmsg)
	}
	return nil
}

func assignScanner(mapvals []interface{}, tagFields map[string]reflect.StructField,
	tag string, index int, key string, obj, value interface{}) {
	switch value.(type) {
	case int:
		mapvals[index] = new(int)
	case int8:
		mapvals[index] = new(int8)
	case int16:
		mapvals[index] = new(int16)
	case int32:
		mapvals[index] = new(int32)
	case int64:
		mapvals[index] = new(int64)
	case uint:
		mapvals[index] = new(uint)
	case uint8:
		mapvals[index] = new(uint8)
	case uint16:
		mapvals[index] = new(uint16)
	case uint32:
		mapvals[index] = new(uint32)
	case uint64:
		mapvals[index] = new(uint64)
	case string:
		mapvals[index] = new(string)
	case float32:
		mapvals[index] = new(float32)
	case float64:
		mapvals[index] = new(float64)
	case bool:
		mapvals[index] = new(bool)
	case []byte:
		mapvals[index] = new([]byte)
	case time.Time:
		mapvals[index] = new(time.Time)
	case sql.Scanner, driver.Valuer, Mapped:
		mapvals[index] = new(interface{})
		typof := reflect.TypeOf(obj).Elem()
		if tag == "" {
			strufield, ok := typof.FieldByName(key)
			if !ok {
				return
			}
			typof = strufield.Type
		} else if strufield, ok := tagFields[key]; ok {
			typof = strufield.Type
		} else {
			for i := 0; i < typof.NumField(); i++ {
				strufield := typof.Field(i)
				if tagval, ok := strufield.Tag.Lookup(tag); ok {
					tagFields[key] = strufield
					if tagHead(tagval) == key {
						typof = strufield.Type
						break
					}
				}
			}
		}

		scannerI := reflect.TypeOf((*sql.Scanner)(nil)).Elem()
		if typof.Implements(scannerI) || reflect.PtrTo(typof).Implements(scannerI) {
			valx := reflect.New(typof).Elem()
			mapvals[index] = valx.Addr().Interface()
		}
	default:
	}

}

func assignValuer(mapres Mapped, tagFields map[string]reflect.StructField,
	tag, key string, obj, value interface{}) {
	switch v := value.(type) {
	case *int8:
		mapres[key] = *v
	case *int16:
		mapres[key] = *v
	case *int32:
		mapres[key] = *v
	case *int64:
		mapres[key] = *v
	case *int:
		mapres[key] = *v
	case *uint8:
		mapres[key] = *v
	case *uint16:
		mapres[key] = *v
	case *uint32:
		mapres[key] = *v
	case *uint64:
		mapres[key] = *v
	case *uint:
		mapres[key] = *v
	case *string:
		mapres[key] = *v
	case *bool:
		mapres[key] = *v
	case *float32:
		mapres[key] = *v
	case *float64:
		mapres[key] = *v
	case *[]byte:
		mapres[key] = *v
	case *time.Time:
		mapres[key] = *v
	case *driver.Valuer:
	default:
		typof := reflect.TypeOf(obj).Elem()
		if tag == "" {
			strufield, ok := typof.FieldByName(key)
			if !ok {
				return
			}
			typof = strufield.Type
		} else if strufield, ok := tagFields[key]; ok {
			typof = strufield.Type
		} else {
			for i := 0; i < typof.NumField(); i++ {
				strufield := typof.Field(i)
				if tagval, ok := strufield.Tag.Lookup(tag); ok {
					if tagHead(tagval) == key {
						typof = strufield.Type
						break
					}
				}
			}
		}
		valuerI := reflect.TypeOf((*driver.Valuer)(nil)).Elem()
		if typof.Implements(valuerI) || reflect.PtrTo(typof).Implements(valuerI) {
			valx := reflect.New(typof).Elem()
			valv := reflect.Indirect(reflect.ValueOf(value))
			valx.Set(valv)
			mapres[key] = valx.Interface()
		}
		// ignore if it's not recognized
	}
}

// SQLScanner is the interface that dictate
// any type that implement Scan method to
// be compatible with sql.Row Scan method.
type SQLScanner interface {
	Scan(dest ...interface{}) error
}

/*
SQLScan is the function that will map scanning object based on provided
field name or field tagged string. The tags can receive the empty string
"" and then it will map the field name by default.
*/
func SQLScan(row SQLScanner, obj interface{}, tag string, x ...string) error {
	mapres := MapTags(obj, tag)
	fieldsName := x
	length := len(x)
	if length == 0 || (length == 1 && x[0] == "*") {
		typof := reflect.TypeOf(obj).Elem()
		newfields := make([]string, typof.NumField())
		length = typof.NumField()
		for i := 0; i < length; i++ {
			field := typof.Field(i)
			if tag == "" {
				newfields[i] = field.Name
			} else {
				if tagval, ok := field.Tag.Lookup(tag); ok {
					newfields[i] = tagHead(tagval)
				}
			}
		}
		fieldsName = newfields
	}
	mapvals := make([]interface{}, length)
	tagFields := make(map[string]reflect.StructField)
	for i, k := range fieldsName {
		assignScanner(mapvals, tagFields, tag, i, k, obj, mapres[k])
	}
	if err := row.Scan(mapvals...); err != nil {
		return err
	}
	for i, k := range fieldsName {
		assignValuer(mapres, tagFields, tag, k, obj, mapvals[i])
	}
	var err error
	if tag == "" {
		err = FillStruct(obj, mapres)
	} else {
		err = FillStructByTags(obj, mapres, tag)
	}
	return err
}
