// Copyright 2023 The Casbin Mesh Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package adapter

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	"go.uber.org/zap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type BadgerTestSuite struct {
	suite.Suite
	db *BadgerStore
}

func (suite *BadgerTestSuite) SetupTest() {
	t := suite.T()

	db, err := NewBadgerStore(zap.NewExample(), testDB)
	if err != nil {
		t.Fatalf("error opening db: %s\n", err.Error())
	}
	suite.db = db

	db.View(func(tx *Tx) error {
		for i := 0; i < 1000; i++ {
			tx.Bucket([]byte("test")).Put([]byte(strconv.Itoa(i)), []byte(strconv.Itoa(i)))
		}
		for i := 0; i < 1000; i++ {
			tx.Bucket([]byte("test")).Put([]byte("nested."+strconv.Itoa(i)), []byte(strconv.Itoa(i)))
		}
		return nil
	})
}

func (suite *BadgerTestSuite) TearDownTest() {
	suite.db.conn.Close()
	if _, err := os.Stat(testDB); err == nil {
		os.RemoveAll(testDB)
	}
}

func Test_BadgerTest_Suite(t *testing.T) {
	suite.Run(t, new(BadgerTestSuite))
}

func (suite *BadgerTestSuite) TestList() {
	err := suite.db.View(func(tx *Tx) error {
		result, err := tx.Bucket([]byte("test")).List("1", 0, 1000, false)
		fmt.Printf("result:%v\n", result)
		assert.Equal(suite.T(), 1000, len(result))
		return err
	})
	assert.Nil(suite.T(), err)
}

func (suite *BadgerTestSuite) TestListReverse() {
	err := suite.db.View(func(tx *Tx) error {
		result, err := tx.Bucket([]byte("test")).List("2", 0, 10, true)
		fmt.Printf("result:%v\n", result)
		assert.Equal(suite.T(), 10, len(result))
		return err
	})
	assert.Nil(suite.T(), err)
}

func (suite *BadgerTestSuite) TestListSkip() {
	err := suite.db.View(func(tx *Tx) error {
		r1, err := tx.Bucket([]byte("test")).List("", 0, 10, false)
		r2, err := tx.Bucket([]byte("test")).List("", 10, 10, false)
		assert.Equal(suite.T(), 10, len(r1))
		assert.Equal(suite.T(), 10, len(r2))
		s, _ := strconv.Atoi(r1[9][0])
		s2, _ := strconv.Atoi(r2[0][0])
		assert.True(suite.T(), s2-s == 1)
		fmt.Printf("result:%v\n", r1)
		fmt.Printf("result:%v\n", r2)
		return err
	})
	assert.Nil(suite.T(), err)
}

func (suite *BadgerTestSuite) TestListWithCursorSkip() {
	err := suite.db.View(func(tx *Tx) error {
		r1, err := tx.Bucket([]byte("test")).List("nested.", 0, 10, false)
		r2, err := tx.Bucket([]byte("test")).List("nested.", 10, 10, false)
		s, _ := strconv.Atoi(r1[9][1])
		s2, _ := strconv.Atoi(r2[0][1])
		assert.True(suite.T(), s2-s == 1)
		assert.Equal(suite.T(), 10, len(r1))
		assert.Equal(suite.T(), 10, len(r2))
		fmt.Printf("result:%v\n", r1)
		fmt.Printf("result:%v\n", r2)
		return err
	})
	assert.Nil(suite.T(), err)
}
