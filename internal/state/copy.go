package state

import "reflect"

// deepCopy returns a copy of v that shares no map, slice or pointer with it.
// One function for every record type, so that a new field is copied without
// anyone remembering to.
func deepCopy[T any](v T) T {
	return copyValue(reflect.ValueOf(&v).Elem()).Interface().(T)
}

// copyValue copies a value and everything it reaches. Unexported struct fields
// are copied as they are: records have none, and the standard types that have
// them (time.Time) do not change.
func copyValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(copyValue(v.Elem()))
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		for iter := v.MapRange(); iter.Next(); {
			out.SetMapIndex(iter.Key(), copyValue(iter.Value()))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(copyValue(v.Index(i)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := range v.NumField() {
			if out.Field(i).CanSet() {
				out.Field(i).Set(copyValue(v.Field(i)))
			}
		}
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(copyValue(v.Elem()))
		return out
	default:
		return v
	}
}
