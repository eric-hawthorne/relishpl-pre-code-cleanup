// Copyright 2012 EveryBitCounts Software Services Inc. All rights reserved.
// Use of this source code is governed by the GNU GPL v3 license, found in the LICENSE_GPL3 file.

// this package is concerned with the expression and management of runtime data (objects and values) 
// in the relish language.

package data

/*
   gc.go -  garbage collection
*/


import (
   . "relish/dbg"
   "sync"
)


var GCMutex sync.RWMutex

var DeferGC int32  // if > 0 means do not GC

var markSense bool = true  // What value of object Marked flag means "is reachable" - if true, 1, if false, 0

func GCMutexRLock(msg string) {
   //Logln(GC_,">>> GCMutex.RLock()")		
   GCMutex.RLock()
}

func GCMutexRUnlock(msg string) {
   //Logln(GC_,"<<< GCMutex.RUnlock()")	
   GCMutex.RUnlock()
}

func GCMutexLock(msg string) {
   //Logln(GC_,">>>>>> GCMutex.Lock()")		
   GCMutex.Lock()
}

func GCMutexUnlock(msg string) {
   //Logln(GC_,"<<<<<< GCMutex.Unlock()")	
   GCMutex.Unlock()
}

/*
Mark the constants as reachable.
*/
func (rt *RuntimeEnv) MarkConstants() {
    defer Un(Trace(GC2_,"MarkConstants"))
	for _,obj := range rt.constants {
		obj.Mark()
	}
    Logln(GC2_,"Marked",len(rt.constants),"constants and their associates.")		
}


/*
Remove from runtime-global maps, those objects which are unreachable from thread stacks or from constants.
By removing these relish-unreachable objects from the maps, the objects become garbage-collectable
by Go.
*/
func (rt *RuntimeEnv) Sweep() {
	
	var nObjs, nObjects, nIds, nIdents, nAtt, nAttrs int
	
	nObjects = len(rt.objects)
	for key, obj := range rt.objects {
	   if obj.IsMarked() != markSense {  // Not reachable
	       delete(rt.objects,key)
	       nObjs++
	   }	
	} 
	nIdents = len(rt.objectIds) 
	for obj := range rt.objectIds {
	   if obj.IsMarked() != markSense {  // Not reachable
		   delete(rt.objectIds,obj)
		   nIds++
	   }	
	}
	for _,attrMap := range rt.attributes {
		nAttrs += len(attrMap)
		for obj := range attrMap {
		   if obj.IsMarked() != markSense {  // Not reachable
			   delete(attrMap,obj)
			   nAtt++
		   }			
		}
	}
	markSense = ! markSense	
	
    Logln(GC2_,"Swept",nObjs,"of",nObjects,"from cache,\n",nIds,"of",nIdents,"from non-persistent ids,\n",nAtt,"of",nAttrs,"attribute associations.")		
}