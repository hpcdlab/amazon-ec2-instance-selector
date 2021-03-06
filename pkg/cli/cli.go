// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//     http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

// Package cli provides functions to build the selector command line interface
package cli

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/aws/amazon-ec2-instance-selector/pkg/selector"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// New creates an instance of CommandLineInterface
func New(binaryName string, shortUsage string, longUsage, examples string) CommandLineInterface {
	rootCmd := &cobra.Command{
		Use:     binaryName,
		Short:   shortUsage,
		Long:    longUsage,
		Example: examples,
		Run:     func(cmd *cobra.Command, args []string) {},
	}
	return CommandLineInterface{
		rootCmd:       rootCmd,
		Flags:         map[string]interface{}{},
		nilDefaults:   map[string]bool{},
		intRangeFlags: map[string]bool{},
		validators:    map[string]validator{},
		suiteFlags:    pflag.NewFlagSet("suite", pflag.ExitOnError),
	}
}

// ParseAndValidateFlags will parse flags registered in this instance of CLI from os.Args
// and then perform validation
func (cl *CommandLineInterface) ParseAndValidateFlags() (map[string]interface{}, error) {
	cl.setUsageTemplate()
	// Remove Suite Flags so that args only include Config and Filter Flags
	cl.rootCmd.SetArgs(cl.removeIntersectingArgs(cl.suiteFlags))
	// This parses Config and Filter flags only
	err := cl.rootCmd.Execute()
	if err != nil {
		return nil, err
	}
	// Remove Config and Filter flags so that only suite flags are parsed
	err = cl.suiteFlags.Parse(cl.removeIntersectingArgs(cl.rootCmd.Flags()))
	if err != nil {
		return nil, err
	}
	// Add suite flags to rootCmd flagset so that other processing can occur
	// This has to be done after usage is printed so that the flagsets can be grouped properly when printed
	cl.rootCmd.Flags().AddFlagSet(cl.suiteFlags)
	err = cl.SetUntouchedFlagValuesToNil()
	if err != nil {
		return nil, err
	}
	err = cl.ProcessRangeFilterFlags()
	if err != nil {
		return nil, err
	}
	err = cl.ValidateFlags()
	if err != nil {
		return nil, err
	}
	return cl.Flags, nil
}

func (cl *CommandLineInterface) removeIntersectingArgs(flagSet *pflag.FlagSet) []string {
	newArgs := os.Args[1:]
	for i, arg := range newArgs {
		if flagSet.Lookup(strings.Replace(arg, "--", "", 1)) != nil || (len(arg) == 2 && flagSet.ShorthandLookup(strings.Replace(arg, "-", "", 1)) != nil) {
			newArgs = append(newArgs[:i], newArgs[i+1:]...)
		}
	}
	return newArgs
}

func (cl *CommandLineInterface) setUsageTemplate() {
	transformedUsage := usageTemplate
	suiteFlagCount := 0
	cl.suiteFlags.VisitAll(func(*pflag.Flag) {
		suiteFlagCount++
	})
	if suiteFlagCount > 0 {
		transformedUsage = fmt.Sprintf(transformedUsage, "\n\nSuite Flags:\n"+cl.suiteFlags.FlagUsages()+"\n")
	} else {
		transformedUsage = fmt.Sprintf(transformedUsage, "")
	}
	cl.rootCmd.SetUsageTemplate(transformedUsage)
	cl.suiteFlags.Usage = func() {}
	cl.rootCmd.Flags().Usage = func() {}
}

// SetUntouchedFlagValuesToNil iterates through all flags and sets their value to nil if they were not specifically set by the user
// This allows for a specified value, a negative value (like false or empty string), or an unspecified (nil) entry.
func (cl *CommandLineInterface) SetUntouchedFlagValuesToNil() error {
	defaultHandlerErrMsg := "Unable to find a default value handler for %v, marking as no default value. This could be an error"
	defaultHandlerFlags := []string{}

	cl.rootCmd.Flags().VisitAll(func(f *pflag.Flag) {
		if !f.Changed {
			// If nilDefaults entry for flag is set to false, do not change default
			if val, _ := cl.nilDefaults[f.Name]; !val {
				return
			}
			switch v := cl.Flags[f.Name].(type) {
			case *int:
				if reflect.ValueOf(*v).IsZero() {
					cl.Flags[f.Name] = nil
				}
			case *string:
				if reflect.ValueOf(*v).IsZero() {
					cl.Flags[f.Name] = nil
				}
			case *bool:
				if reflect.ValueOf(*v).IsZero() {
					cl.Flags[f.Name] = nil
				}
			default:
				defaultHandlerFlags = append(defaultHandlerFlags, f.Name)
				cl.Flags[f.Name] = nil
			}
		}
	})
	if len(defaultHandlerFlags) != 0 {
		return fmt.Errorf(defaultHandlerErrMsg, defaultHandlerFlags)
	}
	return nil
}

// ProcessRangeFilterFlags sets min and max to the appropriate 0 or maxInt bounds based on the 3-tuple that a user specifies for base flag, min, and/or max
func (cl *CommandLineInterface) ProcessRangeFilterFlags() error {
	for flagName := range cl.intRangeFlags {
		rangeHelperMin := fmt.Sprintf("%s-%s", flagName, "min")
		rangeHelperMax := fmt.Sprintf("%s-%s", flagName, "max")
		if cl.Flags[flagName] != nil {
			if cl.Flags[rangeHelperMin] != nil || cl.Flags[rangeHelperMax] != nil {
				return fmt.Errorf("error: --%s and --%s cannot be set when using --%s", rangeHelperMin, rangeHelperMax, flagName)
			}
			cl.Flags[rangeHelperMin] = cl.IntMe(cl.Flags[flagName])
			cl.Flags[rangeHelperMax] = cl.IntMe(cl.Flags[flagName])
		}
		if cl.Flags[rangeHelperMin] == nil && cl.Flags[rangeHelperMax] == nil {
			continue
		} else if cl.Flags[rangeHelperMin] == nil {
			cl.Flags[rangeHelperMin] = cl.IntMe(0)
		} else if cl.Flags[rangeHelperMax] == nil {
			cl.Flags[rangeHelperMax] = cl.IntMe(maxInt)
		}
		cl.Flags[flagName] = &selector.IntRangeFilter{
			LowerBound: *cl.IntMe(cl.Flags[rangeHelperMin]),
			UpperBound: *cl.IntMe(cl.Flags[rangeHelperMax]),
		}
	}
	return nil
}

// ValidateFlags iterates through any registered validators and executes them
func (cl *CommandLineInterface) ValidateFlags() error {
	for flagName, validationFn := range cl.validators {
		if validationFn == nil {
			continue
		}
		err := validationFn(cl.Flags[flagName])
		if err != nil {
			return err
		}
	}
	return nil
}
