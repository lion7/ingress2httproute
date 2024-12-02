package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetOrCreate[K interface{}](ctx context.Context, c client.Client, key types.NamespacedName, owner metav1.OwnerReference, factory func() (*K, error)) (*K, error) {
	resource := new(K)
	obj, ok := interface{}(resource).(client.Object)
	if !ok {
		return nil, nil
	}
	if err := c.Get(ctx, key, obj); err != nil {
		if errors.IsNotFound(err) {
			result, err := factory()
			if err != nil {
				return nil, err
			}
			newObj, ok := interface{}(result).(client.Object)
			if !ok {
				return nil, nil
			}
			newObj.SetNamespace(key.Namespace)
			newObj.SetName(key.Name)
			newObj.SetOwnerReferences([]metav1.OwnerReference{owner})
			if err := c.Create(ctx, newObj); err != nil {
				return nil, err
			}
			return result, nil
		} else {
			return nil, err
		}
	}
	return resource, nil
}

func DeleteIfExists[K interface{}](c client.Client, ctx context.Context, key types.NamespacedName) error {
	resource := new(K)
	obj, ok := interface{}(resource).(client.Object)
	if !ok {
		return nil
	}
	if err := c.Get(ctx, key, obj); err != nil {
		if errors.IsNotFound(err) {
			return nil
		} else {
			return err
		}
	}
	if err := c.Delete(ctx, obj); err != nil {
		return err
	}
	return nil
}
