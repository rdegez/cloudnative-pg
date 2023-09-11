package utils

import (
	"fmt"
)

// HibernateOn hibernate on a cluster
func HibernateOn(
	namespace,
	clusterName string,
) error {
	_, _, err := Run(fmt.Sprintf("kubectl cnpg hibernate on %v -n %v",
		clusterName, namespace))
	if err != nil {
		return err
	}
	return nil
}

// HibernateOff hibernate off a cluster
func HibernateOff(
	namespace,
	clusterName string,
) error {
	_, _, err := Run(fmt.Sprintf("kubectl cnpg hibernate off %v -n %v",
		clusterName, namespace))
	if err != nil {
		return err
	}
	return nil
}
